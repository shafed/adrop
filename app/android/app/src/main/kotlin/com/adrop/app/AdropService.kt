package com.adrop.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import android.util.Log
import com.adrop.core.Platform
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

/**
 * Keeps the core receiving: in the LAN (and through Reticulum hubs when on)
 * while the app is open, and after it is closed when "background" is on
 * (PLAN.md step 4). Owns what only the platform can do: NsdManager, the
 * clipboard, notifications and MediaStore.
 */
class AdropService : Service(), Platform {
    companion object {
        private const val TAG = "adrop"
        private const val CHANNEL_RUN = "running"
        private const val CHANNEL_EVENTS = "events"
        private const val SERVICE_TYPE = "_adrop._tcp."

        fun start(ctx: Context) {
            ctx.startForegroundService(Intent(ctx, AdropService::class.java))
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, AdropService::class.java))
        }
    }

    private val main = Handler(Looper.getMainLooper())
    private val received = mutableListOf<String>()
    private val io = Executors.newSingleThreadExecutor()
    private lateinit var nsd: NsdManager
    private var registration: NsdManager.RegistrationListener? = null
    private var discovery: NsdManager.DiscoveryListener? = null
    private val resolving = Executors.newSingleThreadExecutor() // one resolve at a time before API 34
    private var notifyId = 100

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL_RUN, "Receiving", NotificationManager.IMPORTANCE_MIN))
        nm.createNotificationChannel(NotificationChannel(CHANNEL_EVENTS, "Transfers and pairing", NotificationManager.IMPORTANCE_DEFAULT))
        val tap = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val running = Notification.Builder(this, CHANNEL_RUN)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("adrop is receiving")
            .setContentIntent(tap)
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(1, running, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(1, running)
        }
        nsd = getSystemService(NsdManager::class.java)
        io.execute {
            try {
                val node = Core.start(this, this)
                main.post { advertise(node.name(), node.fingerprint()); discover() }
            } catch (e: Exception) {
                Log.e(TAG, "core: ${e.message}")
                Core.emit(JSONObject().put("kind", "core-error").put("err", e.message).toString())
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int =
        if (Core.setting(this, "background")) START_STICKY else START_NOT_STICKY

    override fun onDestroy() {
        registration?.let { runCatching { nsd.unregisterService(it) } }
        discovery?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        Core.stop()
        super.onDestroy()
    }

    // Platform (called by the Go core) ---------------------------------------

    override fun setClipboard(text: String) {
        main.post {
            getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("adrop", text))
        }
    }

    override fun event(json: String) {
        val e = JSONObject(json)
        when (e.optString("kind")) {
            "recv-file-done" -> {
                val moved = toDownloads(e.optString("path"))
                if (moved != null) {
                    e.put("path", moved)
                    synchronized(received) { received.add(moved) }
                }
            }
            "recv-done" -> {
                val clip = e.optString("file") == "clipboard"
                val what = if (clip) "Clipboard" else "${e.optInt("count")} file(s)"
                val uris = synchronized(received) { received.toList().also { received.clear() } }
                // One received file opens in its default app on tap, as it did
                // in the app this replaces; anything else opens adrop.
                val view = if (!clip && e.optInt("count") == 1) uris.singleOrNull()?.let(::viewIntent) else null
                notify("$what from ${e.optString("peer")}", "Received", view)
            }
            "pair-request" -> {
                val p = e.optJSONObject("pair")
                if (p != null && Core.listeners.isEmpty()) {
                    notify("Pairing request from ${p.optString("name")}", "Code ${p.optString("code")}: open adrop to answer", null)
                }
            }
        }
        Core.emit(e.toString())
    }

    private fun viewIntent(uri: String): Intent? {
        val u = Uri.parse(uri)
        val ext = MimeTypeMap.getFileExtensionFromUrl(uri).lowercase()
        val mime = contentResolver.getType(u)
            ?: MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext)
            ?: "application/octet-stream"
        val view = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(u, mime)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        return view.takeIf { it.resolveActivity(packageManager) != null }
    }

    private fun notify(title: String, text: String, intent: Intent?) {
        val tap = PendingIntent.getActivity(
            this, notifyId, intent ?: Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        getSystemService(NotificationManager::class.java).notify(
            notifyId++,
            Notification.Builder(this, CHANNEL_EVENTS)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(title)
                .setContentText(text)
                .setContentIntent(tap)
                .setAutoCancel(true)
                .build(),
        )
    }

    /**
     * Moves a received file into the shared Downloads (keeping the folder
     * structure under the core's download dir) and returns its content URI.
     */
    private fun toDownloads(path: String): String? {
        val src = File(path)
        if (!src.isFile) return null
        val base = Core.incoming(this)
        val rel = src.parentFile?.relativeTo(base)?.path?.takeIf { it.isNotEmpty() && !it.startsWith("..") }
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, src.name)
            put(MediaStore.Downloads.RELATIVE_PATH, "Download/" + (rel?.let { "$it/" } ?: ""))
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: return null
        return try {
            contentResolver.openOutputStream(uri)!!.use { out -> src.inputStream().use { it.copyTo(out) } }
            contentResolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
            src.delete()
            uri.toString()
        } catch (e: Exception) {
            Log.e(TAG, "MediaStore: ${e.message}")
            contentResolver.delete(uri, null, null)
            null
        }
    }

    // NsdManager: announce this device, report the others to the core -------

    private fun advertise(name: String, fp: String) {
        val info = NsdServiceInfo().apply {
            serviceName = name
            serviceType = SERVICE_TYPE
            port = Core.PORT
            setAttribute("fp", fp)
        }
        val l = object : NsdManager.RegistrationListener {
            override fun onRegistrationFailed(i: NsdServiceInfo, code: Int) { Log.w(TAG, "nsd register: $code") }
            override fun onUnregistrationFailed(i: NsdServiceInfo, code: Int) {}
            override fun onServiceRegistered(i: NsdServiceInfo) { Log.i(TAG, "nsd: announced as ${i.serviceName}") }
            override fun onServiceUnregistered(i: NsdServiceInfo) {}
        }
        registration = l
        runCatching { nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, l) }
    }

    private fun discover() {
        val l = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(t: String) {}
            override fun onDiscoveryStopped(t: String) {}
            override fun onStartDiscoveryFailed(t: String, code: Int) { Log.w(TAG, "nsd discover: $code") }
            override fun onStopDiscoveryFailed(t: String, code: Int) {}
            override fun onServiceFound(i: NsdServiceInfo) = resolving.execute { resolve(i) }
            override fun onServiceLost(i: NsdServiceInfo) {
                Core.node?.removeNearby(i.serviceName)
            }
        }
        discovery = l
        runCatching { nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, l) }
    }

    /** Resolves one service and waits for the answer: NsdManager before API
     *  34 refuses a second resolve while one is running. */
    private fun resolve(info: NsdServiceInfo) {
        val done = java.util.concurrent.CountDownLatch(1)
        nsd.resolveService(info, object : NsdManager.ResolveListener {
            override fun onResolveFailed(i: NsdServiceInfo, code: Int) = done.countDown()
            override fun onServiceResolved(i: NsdServiceInfo) {
                val host = i.host?.hostAddress
                val fp = i.attributes["fp"]?.let { String(it, Charsets.UTF_8) }
                if (host != null && fp != null) Core.node?.addNearby(i.serviceName, host, i.port.toLong(), fp)
                done.countDown()
            }
        })
        done.await(10, java.util.concurrent.TimeUnit.SECONDS)
    }
}
