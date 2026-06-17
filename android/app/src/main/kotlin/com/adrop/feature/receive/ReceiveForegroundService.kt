/**
 * Bounded receive-window foreground service.
 *
 * Started explicitly by the user via "Open to receive" button. Runs a TLS
 * listener for DEFAULT_WINDOW_MS (5 minutes), then tears itself down.
 * Shows a persistent notification with remaining time and a "Stop" action.
 *
 * When a peer connects:
 *   1. Performs Hello exchange.
 *   2. If peer is trusted, hands off to receiveSession().
 *   3. File sessions: saves to Downloads via MediaStore, posts a notification.
 *   4. Clipboard sessions: sets system clipboard, posts a notification.
 *
 * Battery: the service is ONLY active while the window is open and shuts down
 * automatically when the timer expires.
 */
package com.adrop.feature.receive

import android.app.*
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.provider.MediaStore
import android.util.Log
import android.webkit.MimeTypeMap
import androidx.core.app.NotificationCompat
import com.adrop.AdropApplication
import com.adrop.R
import com.adrop.data.identity.IdentityStore
import com.adrop.data.proto.*
import com.adrop.data.trust.TrustRepository
import com.adrop.net.mdns.MdnsManager
import com.adrop.net.session.*
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.*
import com.adrop.ui.MainActivity
import com.adrop.ui.ReceiveWindowState
import kotlinx.coroutines.*
import javax.net.ssl.SSLSocket
import java.net.SocketException
import java.util.concurrent.atomic.AtomicInteger

class ReceiveForegroundService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var windowJob: Job? = null
    private var mdnsManager: MdnsManager? = null

    // The listening server socket and the currently-open client sockets. Closing
    // a blocking socket from another thread is the only reliable way to unblock a
    // coroutine stuck in a socket read/write, so stopWindow() closes these
    // explicitly to abort an in-flight transfer when the user taps Stop.
    @Volatile private var serverSocket: java.net.ServerSocket? = null
    private val openClients = java.util.Collections.synchronizedSet(mutableSetOf<SSLSocket>())

    // Number of transfers currently being received. The receive window will not
    // close while this is > 0, so an in-flight transfer is never cut off.
    private val activeTransfers = AtomicInteger(0)

    // Whether at least one transfer has COMPLETED SUCCESSFULLY during this
    // window. Once that happens the window closes as soon as none remain active
    // (instead of waiting out the idle grace period). A transfer that aborts
    // mid-way does NOT set this, so the window stays open for the rest of the
    // grace period to let the sender reconnect and resume.
    @Volatile private var completedTransfer = false

    // Wall-clock deadline (ms) for the idle grace period: if no transfer has
    // arrived (or an aborted one hasn't reconnected) by then, the window closes.
    // Written from handler coroutines on abort, read by the accept loop.
    @Volatile private var graceDeadlineMs = 0L

    // True while we're counting down toward closing after a transfer aborted
    // (sender cancelled / dropped) and are waiting for a resume reconnect. The
    // notification shows the countdown only in this state. Cleared as soon as a
    // transfer becomes active again.
    @Volatile private var awaitingResume = false

    // ---------------------------------------------------------------------------
    // Lifecycle
    // ---------------------------------------------------------------------------

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startWindow()
            ACTION_STOP  -> stopWindow()
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    // ---------------------------------------------------------------------------
    // Window management
    // ---------------------------------------------------------------------------

    private fun startWindow() {
        completedTransfer = false
        awaitingResume = false
        graceDeadlineMs = System.currentTimeMillis() + GRACE_SEC * 1_000L
        startForeground(NOTIFICATION_ID_SERVICE, buildServiceNotification())
        ReceiveWindowState.onStarted()

        // Refresh the notification once a second so the resume countdown shown
        // after an aborted transfer ticks down. Only active while awaitingResume.
        scope.launch {
            while (isActive) {
                delay(1_000)
                if (awaitingResume) updateServiceNotification()
            }
        }

        val trustRepo = TrustRepository.getInstance(applicationContext)
        mdnsManager = MdnsManager(applicationContext, trustRepo).also { m ->
            m.startDiscovery()
            scope.launch {
                val identity = IdentityStore.getOrCreate(applicationContext)
                m.register(Build.MODEL, LISTEN_PORT, identity.fingerprint)
            }
        }

        windowJob = scope.launch {
            try {
                openReceiveWindow()
            } catch (e: Exception) {
                Log.e(TAG, "receive window error: ${e.message}")
                ReceiveWindowState.onError(e.message ?: "Receive window error")
            } finally {
                ReceiveWindowState.onStopped()
                stopSelf()
            }
        }
    }

    private fun stopWindow() {
        mdnsManager?.let { m ->
            m.stopDiscovery()
            m.unregister()
        }
        mdnsManager = null
        // Close sockets first so any coroutine blocked in a socket read/write
        // (i.e. an in-flight transfer) unblocks and aborts; cancelling the
        // coroutine alone won't interrupt a blocking JVM socket call.
        serverSocket?.let { runCatching { it.close() } }
        serverSocket = null
        synchronized(openClients) {
            openClients.forEach { runCatching { it.close() } }
            openClients.clear()
        }
        windowJob?.cancel()
        ReceiveWindowState.onStopped()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    // ---------------------------------------------------------------------------
    // TLS listener
    // ---------------------------------------------------------------------------

    private suspend fun openReceiveWindow() = withContext(Dispatchers.IO) {
        val identity    = IdentityStore.getOrCreate(applicationContext)
        val trustRepo   = TrustRepository.getInstance(applicationContext)
        val devices     = trustRepo.getAll()
        trustRepo.updateCache(devices)

        val trustManager = PinningTrustManager(
            isTrusted = { fp -> trustRepo.isTrusted(fp) }
        )

        val listenAddr = "0.0.0.0:$LISTEN_PORT"
        val ssLocal = listen(listenAddr, identity, trustManager)
        serverSocket = ssLocal
        Log.i(TAG, "listening on $listenAddr")

        ssLocal.use { ss ->
            // Poll accept() on a short interval so we can re-check the close
            // condition frequently. The window closes when no transfer is active
            // AND either a transfer has already completed (close right after the
            // last one finishes) or the idle grace period elapsed with none
            // arriving. An in-flight transfer always keeps the window open.
            ss.soTimeout = ACCEPT_POLL_MS

            while (isActive) {
                if (activeTransfers.get() == 0 && shouldClose()) break
                try {
                    val client = ss.accept() as SSLSocket
                    // Count the connection as active immediately — before the TLS
                    // handshake — so a connection accepted right at the deadline
                    // can't be torn down in the gap before handleClient marks it.
                    activeTransfers.incrementAndGet()
                    openClients.add(client)
                    awaitingResume = false // a transfer is starting — drop the countdown
                    updateServiceNotification()
                    // Handle each connection in a child coroutine so we can accept the next.
                    launch {
                        try {
                            handleClient(client, trustRepo, identity)
                        } finally {
                            openClients.remove(client)
                            // Transfer ended (cleanly or aborted): refresh the
                            // notification. The next poll closes the window if it
                            // succeeded, or keeps it open for the grace period to
                            // allow a resume reconnect.
                            activeTransfers.decrementAndGet()
                            updateServiceNotification()
                        }
                    }
                } catch (e: SocketException) {
                    if (!isActive) break    // normal shutdown
                    Log.w(TAG, "accept error: ${e.message}")
                } catch (e: java.net.SocketTimeoutException) {
                    // Poll tick: loop back and re-check the close condition.
                }
            }
        }
        serverSocket = null
        // withContext suspends until the child handler coroutines finish, so any
        // transfer still in flight here runs to completion before teardown.
        Log.i(TAG, "receive window closed")
    }

    /**
     * Whether the receive window should close now (only consulted when no
     * transfer is active). Closes immediately once a transfer has completed
     * successfully; otherwise — including after an aborted transfer — waits out
     * the idle grace period so the sender can reconnect and resume.
     */
    private fun shouldClose(): Boolean =
        completedTransfer || System.currentTimeMillis() >= graceDeadlineMs

    private suspend fun handleClient(
        socket: SSLSocket,
        trustRepo: TrustRepository,
        identity: com.adrop.data.identity.DeviceIdentity,
    ) = withContext(Dispatchers.IO) {
        socket.use {
            try {
                it.startHandshake()
                val peerFp = peerFingerprint(it)
                val trusted = trustRepo.isTrusted(peerFp)
                if (trusted == null) {
                    Log.w(TAG, "rejected untrusted peer: ${peerFp.take(16)}")
                    return@withContext
                }

                val inp = it.inputStream.buffered()
                val out = it.outputStream.buffered()

                // Hello exchange: read theirs first, then send ours.
                val hello = readHeader(inp)
                if (hello.type != MsgType.HELLO) {
                    Log.w(TAG, "expected hello from ${trusted.name}, got ${hello.type}")
                    return@withContext
                }
                // Update last-known address from the Hello addr field.
                hello.addr?.let { addr ->
                    trustRepo.updateAddr(peerFp, addr)
                }

                writeControl(out, Header(
                    type        = MsgType.HELLO,
                    version     = PROTOCOL_VERSION,
                    fingerprint = identity.fingerprint,
                    name        = Build.MODEL,
                    addr        = "${localLanIp()}:$LISTEN_PORT",
                ))

                // Receive session. The transfer is already counted as active by
                // the accept loop, so the receive window won't expire mid-transfer.
                val result = receiveSession(
                    context    = applicationContext,
                    inp        = inp,
                    out        = out,
                    peerName   = trusted.name,
                    onClipboard = { bytes, mime -> handleClipboard(bytes, mime, trusted.name) }
                )
                // Reached only on a clean session: mark success so the window
                // can close promptly. An aborted transfer throws above and skips
                // this, leaving the grace period to allow a resume reconnect.
                completedTransfer = true
                notifyResult(result)

            } catch (e: Exception) {
                Log.e(TAG, "client handler error: ${e.message}")
                // Transfer aborted (e.g. sender cancelled or network dropped).
                // Extend the grace period from now so the window stays open long
                // enough for the sender to reconnect and resume, and surface a
                // countdown in the notification while we wait.
                graceDeadlineMs = System.currentTimeMillis() + GRACE_SEC * 1_000L
                awaitingResume = true
            }
        }
    }

    // ---------------------------------------------------------------------------
    // Clipboard
    // ---------------------------------------------------------------------------

    private fun handleClipboard(bytes: ByteArray, mime: String, peerName: String) {
        when (mime) {
            "image/png", "image/jpeg" -> handleClipboardImage(bytes, mime, peerName)
            "text/html"               -> handleClipboardHtml(bytes, peerName)
            else                      -> handleClipboardText(bytes, peerName)
        }
        Log.i(TAG, "clipboard set from $peerName mime=$mime (${bytes.size} bytes)")
    }

    private fun handleClipboardText(bytes: ByteArray, peerName: String) {
        val text = bytes.toString(Charsets.UTF_8)
        val cm = getSystemService(ClipboardManager::class.java)
        cm?.setPrimaryClip(ClipData.newPlainText("adrop from $peerName", text))
    }

    private fun handleClipboardHtml(bytes: ByteArray, peerName: String) {
        val html = bytes.toString(Charsets.UTF_8)
        val cm = getSystemService(ClipboardManager::class.java)
        cm?.setPrimaryClip(ClipData.newHtmlText("adrop from $peerName", html, html))
    }

    private fun handleClipboardImage(bytes: ByteArray, mime: String, peerName: String) {
        val ext = if (mime == "image/png") "png" else "jpg"
        val fileName = "adrop_${System.currentTimeMillis()}.$ext"
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, fileName)
            put(MediaStore.Images.Media.MIME_TYPE, mime)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                put(MediaStore.Images.Media.IS_PENDING, 1)
            }
        }
        val collection = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            MediaStore.Images.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        } else {
            MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        }
        val resolver = contentResolver
        val uri = resolver.insert(collection, values) ?: run {
            Log.e(TAG, "MediaStore insert failed for image from $peerName")
            return
        }
        try {
            resolver.openOutputStream(uri)!!.use { it.write(bytes) }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val done = ContentValues().apply { put(MediaStore.Images.Media.IS_PENDING, 0) }
                resolver.update(uri, done, null, null)
            }
            // Post a notification; gallery apps will pick up the new image automatically.
            postImageNotification(peerName)
        } catch (e: Exception) {
            resolver.delete(uri, null, null)
            Log.e(TAG, "failed to save image from $peerName: ${e.message}")
        }
    }

    private fun postImageNotification(peerName: String) {
        val nm = getSystemService(NotificationManager::class.java) ?: return
        val openIntent = Intent(this, MainActivity::class.java)
        val pi = PendingIntent.getActivity(
            this, 0, openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(this, AdropApplication.CHANNEL_TRANSFERS)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Image from $peerName")
            .setContentText("Image copied to Gallery")
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()
        nm.notify(System.currentTimeMillis().toInt(), notification)
    }

    // ---------------------------------------------------------------------------
    // Notifications
    // ---------------------------------------------------------------------------

    private fun notifyResult(result: SessionResult) {
        val nm = getSystemService(NotificationManager::class.java) ?: return
        val (title, body) = when (result) {
            is SessionResult.Files -> {
                val count = result.files.size
                "Received $count file(s) from ${result.peerName}" to
                    result.files.joinToString(", ") { it.name }
            }
            is SessionResult.Clipboard -> {
                val preview = when {
                    result.mime.startsWith("image/") -> "Image (${result.bytes.size} bytes)"
                    else -> result.bytes.toString(Charsets.UTF_8).take(60)
                }
                "Clipboard from ${result.peerName}" to preview
            }
        }

        // Tapping the notification opens the file directly when a single file
        // arrived; otherwise it just opens the app.
        val pi = contentIntentFor(result)

        val notification = NotificationCompat.Builder(this, AdropApplication.CHANNEL_TRANSFERS)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()

        nm.notify(System.currentTimeMillis().toInt(), notification)
    }

    /**
     * Builds the notification tap intent. A single received file opens via
     * ACTION_VIEW on its MediaStore URI (with read permission granted to the
     * handling app); anything else falls back to launching MainActivity.
     */
    private fun contentIntentFor(result: SessionResult): PendingIntent {
        val single = (result as? SessionResult.Files)
            ?.files
            ?.singleOrNull()
            ?.takeIf { it.mediaStoreUri != null }

        if (single != null) {
            val ext = single.name.substringAfterLast('.', "").lowercase()
            val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext)
                ?: "application/octet-stream"
            val view = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(single.mediaStoreUri, mime)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            // Only use it if some app can handle it; otherwise fall through.
            if (view.resolveActivity(packageManager) != null) {
                return PendingIntent.getActivity(
                    this, single.mediaStoreUri.hashCode(), view,
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
                )
            }
        }

        return PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun buildServiceNotification(): Notification {
        val stopIntent = Intent(this, ReceiveForegroundService::class.java)
            .setAction(ACTION_STOP)
        val stopPi = PendingIntent.getService(
            this, 1, stopIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val active = activeTransfers.get()
        val text = when {
            active == 1 -> "Receiving a transfer…"
            active > 1  -> "Receiving $active transfers…"
            awaitingResume -> {
                // Transfer was interrupted; counting down to close while we wait
                // for the sender to reconnect and resume.
                val secs = ((graceDeadlineMs - System.currentTimeMillis()) / 1_000)
                    .coerceAtLeast(0)
                "Transfer interrupted — closing in ${secs}s"
            }
            else        -> "Ready to receive"
        }

        return NotificationCompat.Builder(this, CHANNEL_SERVICE)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Receive window open")
            .setContentText(text)
            .setOngoing(true)
            .addAction(android.R.drawable.ic_media_pause, "Stop", stopPi)
            .build()
    }

    private fun updateServiceNotification() {
        val nm = getSystemService(NotificationManager::class.java) ?: return
        nm.notify(NOTIFICATION_ID_SERVICE, buildServiceNotification())
    }

    // ---------------------------------------------------------------------------
    // Companion
    // ---------------------------------------------------------------------------

    companion object {
        private const val TAG = "ReceiveService"

        const val CHANNEL_SERVICE      = "adrop_receive_window"
        const val NOTIFICATION_ID_SERVICE = 100
        const val LISTEN_PORT          = 7777
        // Idle grace period: how long the window stays open waiting for the
        // first transfer to arrive. Once any transfer completes the window
        // closes immediately regardless of this. Sized to comfortably cover an
        // FCM-wake round-trip (the daemon retries ~10s after waking the phone).
        const val GRACE_SEC            = 60
        const val ACCEPT_POLL_MS       = 1_000 // accept() poll interval

        const val ACTION_START = "com.adrop.RECEIVE_START"
        const val ACTION_STOP  = "com.adrop.RECEIVE_STOP"

        fun startIntent(context: Context): Intent =
            Intent(context, ReceiveForegroundService::class.java).setAction(ACTION_START)

        fun stopIntent(context: Context): Intent =
            Intent(context, ReceiveForegroundService::class.java).setAction(ACTION_STOP)
    }
}
