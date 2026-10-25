package com.adrop.adrop_app

import android.os.Build
import android.os.Handler
import android.os.Looper
import com.adrop.proto.core.Core
import com.adrop.proto.core.Node
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.util.concurrent.Executors

// Hands the Flutter side's calls to the Go core. Core calls block on the
// network, so they run on one worker thread and answer on the main thread.
class MainActivity : FlutterActivity() {
    private val worker = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())
    private var node: Node? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "adrop/core")
            .setMethodCallHandler { call, result ->
                worker.execute {
                    try {
                        val value: String = when (call.method) {
                            "start" -> {
                                val n = node ?: Core.start(
                                    File(filesDir, "core").path,
                                    "${Build.MODEL} proto",
                                ).also { node = it }
                                n.fingerprint()
                            }
                            "pair" -> started().pair(call.arguments as String)
                            "peers" -> started().peers()
                            "hello" -> started().hello(call.arguments as String)
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

    private fun started(): Node = node ?: throw IllegalStateException("core is not started")
}
