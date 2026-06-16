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
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import com.adrop.AdropApplication
import com.adrop.R
import com.adrop.data.identity.IdentityStore
import com.adrop.data.proto.*
import com.adrop.data.trust.TrustRepository
import com.adrop.net.session.*
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.*
import com.adrop.ui.MainActivity
import kotlinx.coroutines.*
import javax.net.ssl.SSLSocket
import java.net.SocketException

class ReceiveForegroundService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var windowJob: Job? = null
    private var timerJob: Job? = null
    private var remainingSeconds = DEFAULT_WINDOW_SEC

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
        remainingSeconds = DEFAULT_WINDOW_SEC
        startForeground(NOTIFICATION_ID_SERVICE, buildServiceNotification(remainingSeconds))

        windowJob = scope.launch {
            try {
                openReceiveWindow()
            } catch (e: Exception) {
                Log.e(TAG, "receive window error: ${e.message}")
            } finally {
                stopSelf()
            }
        }

        // Countdown timer — updates the notification every second.
        timerJob = scope.launch {
            while (remainingSeconds > 0) {
                delay(1_000)
                remainingSeconds--
                if (remainingSeconds % 5 == 0) {   // update every 5 s to reduce churn
                    updateServiceNotification(remainingSeconds)
                }
            }
        }
    }

    private fun stopWindow() {
        windowJob?.cancel()
        timerJob?.cancel()
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
        val serverSocket = listen(listenAddr, identity, trustManager)
        Log.i(TAG, "listening on $listenAddr")

        serverSocket.use { ss ->
            // Time out the accept loop when the window expires.
            val windowMs = DEFAULT_WINDOW_SEC * 1_000L
            ss.soTimeout = windowMs.toInt().coerceAtMost(Int.MAX_VALUE)

            while (isActive) {
                try {
                    val client = ss.accept() as SSLSocket
                    // Handle each connection in a child coroutine so we can accept the next.
                    launch {
                        handleClient(client, trustRepo, identity)
                    }
                } catch (e: SocketException) {
                    if (!isActive) break    // normal shutdown
                    Log.w(TAG, "accept error: ${e.message}")
                } catch (e: java.net.SocketTimeoutException) {
                    // Window expired.
                    break
                }
            }
        }
        Log.i(TAG, "receive window closed")
    }

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

                // Receive session.
                val result = receiveSession(
                    context    = applicationContext,
                    inp        = inp,
                    out        = out,
                    peerName   = trusted.name,
                    onClipboard = { bytes, mime -> handleClipboard(bytes, mime, trusted.name) }
                )
                notifyResult(result)

            } catch (e: Exception) {
                Log.e(TAG, "client handler error: ${e.message}")
            }
        }
    }

    // ---------------------------------------------------------------------------
    // Clipboard
    // ---------------------------------------------------------------------------

    private fun handleClipboard(bytes: ByteArray, mime: String, peerName: String) {
        val text = bytes.toString(Charsets.UTF_8)
        val cm = getSystemService(ClipboardManager::class.java)
        cm?.setPrimaryClip(ClipData.newPlainText("adrop from $peerName", text))
        Log.i(TAG, "clipboard set from $peerName (${bytes.size} bytes)")
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
                val preview = result.bytes.toString(Charsets.UTF_8).take(60)
                "Clipboard from ${result.peerName}" to preview
            }
        }

        val openIntent = Intent(this, MainActivity::class.java)
        val pi = PendingIntent.getActivity(
            this, 0, openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

        val notification = NotificationCompat.Builder(this, AdropApplication.CHANNEL_TRANSFERS)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()

        nm.notify(System.currentTimeMillis().toInt(), notification)
    }

    private fun buildServiceNotification(remainingSec: Int): Notification {
        val stopIntent = Intent(this, ReceiveForegroundService::class.java)
            .setAction(ACTION_STOP)
        val stopPi = PendingIntent.getService(
            this, 1, stopIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val min = remainingSec / 60
        val sec = remainingSec % 60
        val timeStr = if (min > 0) "${min}m ${sec}s" else "${sec}s"

        return NotificationCompat.Builder(this, CHANNEL_SERVICE)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Receive window open")
            .setContentText("Listening for transfers — $timeStr remaining")
            .setOngoing(true)
            .addAction(android.R.drawable.ic_media_pause, "Stop", stopPi)
            .build()
    }

    private fun updateServiceNotification(remainingSec: Int) {
        val nm = getSystemService(NotificationManager::class.java) ?: return
        nm.notify(NOTIFICATION_ID_SERVICE, buildServiceNotification(remainingSec))
    }

    // ---------------------------------------------------------------------------
    // Companion
    // ---------------------------------------------------------------------------

    companion object {
        private const val TAG = "ReceiveService"

        const val CHANNEL_SERVICE      = "adrop_receive_window"
        const val NOTIFICATION_ID_SERVICE = 100
        const val LISTEN_PORT          = 7777
        const val DEFAULT_WINDOW_SEC   = 300  // 5 minutes

        const val ACTION_START = "com.adrop.RECEIVE_START"
        const val ACTION_STOP  = "com.adrop.RECEIVE_STOP"

        fun startIntent(context: Context): Intent =
            Intent(context, ReceiveForegroundService::class.java).setAction(ACTION_START)

        fun stopIntent(context: Context): Intent =
            Intent(context, ReceiveForegroundService::class.java).setAction(ACTION_STOP)
    }
}
