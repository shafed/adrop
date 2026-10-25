package com.adrop.app

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.util.concurrent.Executors

/**
 * Hands the Flutter UI's calls to the core in AdropService. Core calls may
 * block on the network, so they run on a worker pool and answer on the main
 * thread. Also takes "Share" intents and adrop://pair links.
 */
class MainActivity : FlutterActivity() {
    companion object {
        private const val PICK_FOLDER = 7
    }

    private val worker = Executors.newCachedThreadPool()
    private val main = Handler(Looper.getMainLooper())
    private val shared = mutableListOf<String>()
    private var events: ((String) -> Unit)? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        AdropService.start(this)
        handleIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onDestroy() {
        events?.let { Core.listeners.remove(it) }
        if (isFinishing && !Core.setting(this, "background")) AdropService.stop(this)
        super.onDestroy()
    }

    private fun handleIntent(intent: Intent?) {
        when (intent?.action) {
            Intent.ACTION_SEND -> {
                val uri = intent.parcelable<Uri>(Intent.EXTRA_STREAM)
                if (uri != null) {
                    worker.execute { copyShared(listOf(uri)) }
                } else {
                    intent.getStringExtra(Intent.EXTRA_TEXT)?.let { text ->
                        val f = File(cacheDir, "shared/text.txt").apply { parentFile?.mkdirs(); writeText(text) }
                        synchronized(shared) { shared.add(f.path) }
                    }
                }
            }
            Intent.ACTION_SEND_MULTIPLE -> {
                val uris = intent.parcelableList<Uri>(Intent.EXTRA_STREAM) ?: return
                worker.execute { copyShared(uris) }
            }
            Intent.ACTION_VIEW -> {
                val link = intent.dataString ?: return
                if (link.startsWith("adrop://pair")) {
                    worker.execute {
                        val msg = runCatching { "paired with " + Core.require().pair(link) }.getOrElse { it.message ?: "$it" }
                        Core.emit("""{"kind":"paired","peer":${org.json.JSONObject.quote(msg)}}""")
                    }
                }
            }
        }
    }

    /** Copies shared content into the cache, where the core can read it as files. */
    private fun copyShared(uris: List<Uri>) {
        val dir = File(cacheDir, "shared").apply { mkdirs() }
        for (uri in uris) {
            val name = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            } ?: uri.lastPathSegment ?: "shared"
            val dst = File(dir, name.replace('/', '_'))
            contentResolver.openInputStream(uri)?.use { input -> dst.outputStream().use { input.copyTo(it) } }
            synchronized(shared) { shared.add(dst.path) }
        }
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val messenger = flutterEngine.dartExecutor.binaryMessenger
        EventChannel(messenger, "adrop/events").setStreamHandler(object : EventChannel.StreamHandler {
            override fun onListen(arguments: Any?, sink: EventChannel.EventSink) {
                val l: (String) -> Unit = { json -> main.post { sink.success(json) } }
                events = l
                Core.listeners.add(l)
            }

            override fun onCancel(arguments: Any?) {
                events?.let { Core.listeners.remove(it) }
                events = null
            }
        })
        MethodChannel(messenger, "adrop/core").setMethodCallHandler { call, result ->
            worker.execute {
                try {
                    val value: Any? = when (call.method) {
                        "status" -> Core.require().status()
                        "devices" -> Core.require().devices()
                        "nearby" -> Core.require().nearby()
                        "rename" -> Core.require().rename(call.argument<String>("target"), call.argument<String>("name")).let { null }
                        "revoke" -> Core.require().revoke(call.arguments as String).let { null }
                        "showPairing" -> Core.require().run { openPairWindow(300); pairingURI() }
                        "pair" -> Core.require().pair(call.arguments as String)
                        "pairWith" -> Core.require().pairWith(call.arguments as String)
                        "pendingPairs" -> Core.require().pendingPairs()
                        "confirmPair" -> Core.require().confirmPair(call.argument<String>("id"), call.argument<Boolean>("ok") == true).let { null }
                        "sendFiles" -> Core.require().sendFiles(call.argument<String>("target"), call.argument<String>("paths"))
                        "sendText" -> Core.require().sendText(call.argument<String>("target"), call.argument<String>("text"))
                        "queue" -> Core.require().queue()
                        "unqueue" -> Core.require().unqueue(call.arguments as String).let { null }
                        "ping" -> Core.require().ping(call.arguments as String)
                        "getSetting" -> Core.setting(this, call.arguments as String)
                        "setSetting" -> {
                            val key = call.argument<String>("key")!!
                            Core.prefs(this).edit().putBoolean(key, call.argument<Boolean>("value") == true).apply()
                            if (key != "background") restartCore()
                            null
                        }
                        "takeShared" -> synchronized(shared) { shared.toList().also { shared.clear() } }
                        "open" -> open(call.arguments as String).let { null }
                        "pickFolder" -> {
                            main.post {
                                pendingFolder = result
                                startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), PICK_FOLDER)
                            }
                            return@execute
                        }
                        else -> {
                            main.post { result.notImplemented() }
                            return@execute
                        }
                    }
                    main.post { result.success(value) }
                } catch (e: Exception) {
                    main.post { result.error("core", e.message, null) }
                }
            }
        }
    }

    private var pendingFolder: MethodChannel.Result? = null

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != PICK_FOLDER) return
        val result = pendingFolder ?: return
        pendingFolder = null
        val tree = data?.data
        if (resultCode != RESULT_OK || tree == null) {
            result.success(null)
            return
        }
        worker.execute {
            try {
                val path = copyTree(tree)
                main.post { result.success(path) }
            } catch (e: Exception) {
                main.post { result.error("folder", e.message, null) }
            }
        }
    }

    /** Copies a picked document tree into the cache, keeping its structure. */
    private fun copyTree(tree: Uri): String {
        val rootId = DocumentsContract.getTreeDocumentId(tree)
        val root = File(cacheDir, "shared/" + (rootId.substringAfterLast(':').substringAfterLast('/').ifEmpty { "folder" }))
        root.deleteRecursively()
        fun walk(docId: String, dir: File) {
            dir.mkdirs()
            val children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, docId)
            contentResolver.query(
                children,
                arrayOf(
                    DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                    DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                    DocumentsContract.Document.COLUMN_MIME_TYPE,
                ),
                null, null, null,
            )?.use { c ->
                while (c.moveToNext()) {
                    val id = c.getString(0)
                    val name = c.getString(1).replace('/', '_')
                    if (c.getString(2) == DocumentsContract.Document.MIME_TYPE_DIR) {
                        walk(id, File(dir, name))
                    } else {
                        val uri = DocumentsContract.buildDocumentUriUsingTree(tree, id)
                        contentResolver.openInputStream(uri)?.use { input -> File(dir, name).outputStream().use { input.copyTo(it) } }
                    }
                }
            }
        }
        walk(rootId, root)
        return root.path
    }

    private fun restartCore() {
        AdropService.stop(this)
        Thread.sleep(500)
        AdropService.start(this)
        for (i in 0 until 50) {
            if (Core.node != null) return
            Thread.sleep(100)
        }
    }

    private fun open(path: String) {
        val uri = Uri.parse(path)
        val view = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, contentResolver.getType(uri))
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        startActivity(Intent.createChooser(view, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
}

@Suppress("DEPRECATION")
private inline fun <reified T : android.os.Parcelable> Intent.parcelable(key: String): T? =
    if (Build.VERSION.SDK_INT >= 33) getParcelableExtra(key, T::class.java) else getParcelableExtra(key)

@Suppress("DEPRECATION")
private inline fun <reified T : android.os.Parcelable> Intent.parcelableList(key: String): List<T>? =
    if (Build.VERSION.SDK_INT >= 33) getParcelableArrayListExtra(key, T::class.java) else getParcelableArrayListExtra(key)
