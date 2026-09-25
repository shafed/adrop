/**
 * Shared low-level send routines used by both the interactive [SendViewModel]
 * and the background [com.adrop.feature.send.SendWorker].
 *
 * These operate on already-materialised [File]s on local storage (not SAF
 * content URIs), so they can run from a WorkManager worker long after the
 * picker that produced the original URIs is gone.
 */
package com.adrop.feature.send

import android.content.Context
import android.os.Build
import com.adrop.data.identity.IdentityStore
import com.adrop.data.proto.*
import com.adrop.data.trust.TrustedDevice
import com.adrop.feature.fcm.FcmTokenStore
import com.adrop.net.session.ProgressFn
import com.adrop.net.session.buildManifest
import com.adrop.net.session.sendClipboard
import com.adrop.net.session.sendFiles
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.dial
import com.adrop.net.transport.localLanIp
import java.io.File

/**
 * Sends [files] (local paths) to [device] over a freshly dialled mTLS socket.
 *
 * The display name sent in the manifest comes from each File's own name, so
 * callers must name the copied files with the user-facing filename.
 *
 * Throws on any network/protocol error (so the caller can decide whether to
 * queue / retry).
 */
fun sendFilesNow(
    context: Context,
    device: TrustedDevice,
    files: List<File>,
    trustMgr: PinningTrustManager,
    /** User-facing names for the manifest; defaults to each file's own name. */
    displayNames: List<String> = files.map { it.name },
    onPreparing: ProgressFn? = null,
    progress: ProgressFn? = null,
) {
    val identity = IdentityStore.getOrCreate(context)

    onPreparing?.invoke("Hashing selected files…")
    require(files.size == displayNames.size)
    val sources = mutableListOf<File>()
    val manifest = mutableListOf<FileMeta>()
    fun add(file: File, relative: String?, name: String) {
        com.adrop.net.session.pathParts(relative ?: name)
        if (file.isDirectory) {
            manifest.add(FileMeta(name = name, size = 0, sha256 = "", relPath = relative ?: name, isDir = true))
            sources.add(file)
            val base = relative ?: name
            val children = file.listFiles() ?: error("Cannot read $name")
            children.sortedBy { it.name }.forEach { add(it, "$base/${it.name}", it.name) }
        } else {
            check(file.isFile) { "Not a regular file: $name" }
            val meta = buildManifest(listOf(name), listOf(file.length())) { file.inputStream() }.single()
            manifest.add(meta.copy(relPath = relative))
            sources.add(file)
        }
    }
    files.forEachIndexed { i, file -> add(file, null, displayNames[i]) }

    onPreparing?.invoke("Connecting to ${device.name}…")
    val socket = dial(device.addr, identity, trustMgr)
    socket.use { s ->
        val out = s.outputStream.buffered()
        val inp = s.inputStream.buffered()
        writeHello(context, out, identity)
        val hello = readHeader(inp)
        check(manifest.none { it.isDir || it.relPath != null } || hello.folders) { "Peer does not support folders; update the receiving app" }

        runBlockingSendFiles(out, inp, manifest, sources, progress)
    }
}

/**
 * Sends raw clipboard [bytes] of [mime] to [device].
 */
fun sendClipboardNow(
    context: Context,
    device: TrustedDevice,
    bytes: ByteArray,
    mime: String,
    trustMgr: PinningTrustManager,
) {
    val identity = IdentityStore.getOrCreate(context)
    val socket = dial(device.addr, identity, trustMgr)
    socket.use { s ->
        val out = s.outputStream.buffered()
        val inp = s.inputStream.buffered()
        writeHello(context, out, identity)
        readHeader(inp)

        sendClipboard(out = out, inp = inp, text = bytes, mime = mime)
    }
}

private fun writeHello(
    context: Context,
    out: java.io.OutputStream,
    identity: com.adrop.data.identity.DeviceIdentity,
) {
    writeControl(out, Header(
        type        = MsgType.HELLO,
        version     = PROTOCOL_VERSION,
        fingerprint = identity.fingerprint,
        name        = Build.MODEL,
        addr        = "${localLanIp()}:${com.adrop.feature.receive.ReceiveForegroundService.LISTEN_PORT}",
        fcmToken    = FcmTokenStore.load(context),
    ))
}

// sendFiles is a suspend fun; the call sites here already run on a background
// dispatcher (Dispatchers.IO / a WorkManager worker thread), so bridging via
// runBlocking is safe and keeps the public helpers non-suspending.
private fun runBlockingSendFiles(
    out: java.io.OutputStream,
    inp: java.io.InputStream,
    manifest: List<FileMeta>,
    files: List<File>,
    progress: ProgressFn?,
) = kotlinx.coroutines.runBlocking {
    sendFiles(
        out      = out,
        inp      = inp,
        manifest = manifest,
        openFile = { i -> files[i].inputStream() },
        progress = progress,
    )
}
