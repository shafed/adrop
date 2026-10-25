package com.adrop.app

import android.content.Context
import android.os.Build
import android.util.Log
import com.adrop.core.Core as GoCore
import com.adrop.core.Node
import com.adrop.core.Options
import com.adrop.core.Platform
import java.io.File
import java.util.concurrent.CopyOnWriteArraySet

/**
 * The Go core (core/, gomobile) for the whole process: AdropService starts
 * it, MainActivity calls into it. Events fan out to whoever listens.
 */
object Core {
    private const val TAG = "adrop"
    const val PORT = 53127

    @Volatile
    var node: Node? = null
        private set

    val listeners = CopyOnWriteArraySet<(String) -> Unit>()

    fun prefs(ctx: Context) = ctx.getSharedPreferences("adrop", Context.MODE_PRIVATE)

    fun setting(ctx: Context, key: String) = prefs(ctx).getBoolean(key, false)

    /** Received files land here, then move to MediaStore Downloads. */
    fun incoming(ctx: Context) = File(ctx.filesDir, "incoming")

    @Synchronized
    fun start(ctx: Context, platform: Platform): Node {
        node?.let { return it }
        val opt = Options().apply {
            dataDir = File(ctx.filesDir, "core").path
            downloadDir = incoming(ctx).path
            name = deviceName(ctx)
            port = PORT.toLong()
            tapPair = setting(ctx, "tapPair")
            reticulum = setting(ctx, "reticulum")
        }
        return GoCore.start(opt, platform).also {
            node = it
            Log.i(TAG, "core started: ${it.fingerprint()}")
        }
    }

    @Synchronized
    fun stop() {
        node?.stop()
        node = null
    }

    fun require(): Node = node ?: throw IllegalStateException("adrop is not running")

    fun emit(json: String) = listeners.forEach { it(json) }

    private fun deviceName(ctx: Context): String =
        prefs(ctx).getString("name", null) ?: Build.MODEL
}
