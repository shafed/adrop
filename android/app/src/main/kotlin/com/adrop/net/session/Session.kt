/**
 * High-level session state machines — send and receive.
 *
 * Mirrors the logic in the Go daemon's daemon/send.go and daemon/receive.go.
 * All I/O is done on the calling coroutine; call from Dispatchers.IO.
 */
package com.adrop.net.session

import android.content.ContentValues
import android.content.Context
import android.os.Build
import android.provider.MediaStore
import com.adrop.data.proto.*
import java.io.InputStream
import java.io.OutputStream
import java.security.MessageDigest

// ---------------------------------------------------------------------------
// Progress callbacks
// ---------------------------------------------------------------------------

typealias ProgressFn = (message: String) -> Unit

/**
 * Called with per-file byte-level progress from a TypeProgress frame.
 *
 * [fileIndex] indexes into the session manifest.
 * [bytesDone] is how many bytes of the file have been transferred so far.
 * [totalBytes] is the total file size.
 *
 * This callback is invoked on the same thread as the I/O (Dispatchers.IO).
 * Callers should post updates to a StateFlow or LiveData for UI consumption.
 */
typealias FileProgressFn = (fileIndex: Int, bytesDone: Long, totalBytes: Long) -> Unit

// ---------------------------------------------------------------------------
// Sender
// ---------------------------------------------------------------------------

/**
 * Sends a file session to an already-open [OutputStream] / [InputStream] pair.
 *
 * Caller must have already completed the Hello exchange before calling this.
 *
 * After each chunk is written, a [MsgType.PROGRESS] frame is sent to the peer
 * so the remote side can show per-file progress. The [fileProgress] callback
 * (if supplied) is also invoked locally with the same numbers.
 */
suspend fun sendFiles(
    out: OutputStream,
    inp: InputStream,
    manifest: List<FileMeta>,
    openFile: (index: Int) -> InputStream,
    progress: ProgressFn? = null,
    fileProgress: FileProgressFn? = null,
) {
    // SessionStart
    writeControl(out, Header(
        type  = MsgType.SESSION_START,
        kind  = SessionKind.FILES,
        files = manifest,
    ))

    for ((i, meta) in manifest.withIndex()) {
        progress?.invoke("Sending ${meta.name} (${i + 1}/${manifest.size})")
        // FileHeader
        writeControl(out, Header(type = MsgType.FILE_HEADER, fileIndex = i))

        // Chunks — emit a TypeProgress frame after each one.
        var bytesSent = 0L
        openFile(i).use { stream ->
            val buf = ByteArray(CHUNK_SIZE)
            while (true) {
                val n = stream.read(buf)
                if (n < 0) break
                writeMessage(
                    out,
                    Header(type = MsgType.CHUNK, fileIndex = i, length = n.toLong()),
                    buf.inputStream(0, n),
                )
                bytesSent += n
                // Send advisory progress frame (best-effort; no payload).
                writeControl(out, Header(
                    type       = MsgType.PROGRESS,
                    fileIndex  = i,
                    bytesDone  = bytesSent,
                    totalBytes = meta.size,
                ))
                fileProgress?.invoke(i, bytesSent, meta.size)
            }
        }

        // FileEnd
        writeControl(out, Header(type = MsgType.FILE_END, fileIndex = i))

        // Wait for per-file ack
        val ack = readHeader(inp)
        if (ack.type != MsgType.ACK) {
            throw SessionException("expected ack for file $i, got ${ack.type}")
        }
        if (ack.ok != true) {
            throw SessionException("peer rejected ${meta.name}: ${ack.error}")
        }
    }

    // SessionEnd
    writeControl(out, Header(type = MsgType.SESSION_END))

    // Final session ack
    val finalAck = readHeader(inp)
    if (finalAck.ok != true) {
        throw SessionException("peer rejected session: ${finalAck.error}")
    }

    progress?.invoke("Sent ${manifest.size} file(s)")
}

/**
 * Sends a clipboard session.
 */
fun sendClipboard(
    out: OutputStream,
    inp: InputStream,
    text: ByteArray,
    mime: String = "text/plain",
) {
    writeControl(out, Header(type = MsgType.SESSION_START, kind = SessionKind.CLIPBOARD))
    writeMessage(out, Header(type = MsgType.CLIPBOARD, mime = mime, length = text.size.toLong()),
        text.inputStream())
    writeControl(out, Header(type = MsgType.SESSION_END))

    val ack = readHeader(inp)
    if (ack.ok != true) {
        throw SessionException("peer rejected clipboard: ${ack.error}")
    }
}

// ---------------------------------------------------------------------------
// Receiver
// ---------------------------------------------------------------------------

/** Result of receiving one file. */
data class ReceivedFile(
    val name: String,
    val mediaStoreUri: android.net.Uri?,
)

/**
 * Receives a full session (after the Hello exchange has been done by the caller).
 *
 * For file sessions, files are saved to the shared Downloads directory via MediaStore.
 * For clipboard sessions, [onClipboard] is invoked with the raw bytes + MIME.
 *
 * [onProgress] is called whenever a [MsgType.PROGRESS] frame is received from
 * the sender (i.e. when the phone is the receiver and the PC sent the file).
 * It is safe to post from this callback to a StateFlow for UI updates.
 *
 * Returns a [SessionResult] describing what arrived.
 */
fun receiveSession(
    context: Context,
    inp: InputStream,
    out: OutputStream,
    peerName: String,
    onClipboard: (bytes: ByteArray, mime: String) -> Unit,
    onProgress: FileProgressFn? = null,
): SessionResult {
    val start = readHeader(inp)
    if (start.type != MsgType.SESSION_START) {
        throw SessionException("expected session_start, got ${start.type}")
    }
    return when (start.kind) {
        SessionKind.FILES     -> receiveFiles(context, inp, out, peerName, start.files ?: emptyList(), onProgress)
        SessionKind.CLIPBOARD -> receiveClipboard(inp, out, peerName, onClipboard)
        else -> throw SessionException("unknown session kind: ${start.kind}")
    }
}

private fun receiveFiles(
    context: Context,
    inp: InputStream,
    out: OutputStream,
    peerName: String,
    manifest: List<FileMeta>,
    onProgress: FileProgressFn? = null,
): SessionResult {
    if (manifest.isEmpty()) throw SessionException("empty file manifest")

    val received = mutableListOf<ReceivedFile>()

    while (true) {
        val hdr = readHeader(inp)
        if (hdr.type == MsgType.SESSION_END) break
        // TypeProgress frames may appear between file_header messages; forward
        // them to the caller and keep waiting for the next file_header.
        if (hdr.type == MsgType.PROGRESS) {
            val fi = hdr.fileIndex ?: 0
            val done = hdr.bytesDone ?: 0L
            val total = hdr.totalBytes ?: 0L
            onProgress?.invoke(fi, done, total)
            continue
        }
        if (hdr.type != MsgType.FILE_HEADER) {
            throw SessionException("expected file_header, got ${hdr.type}")
        }
        val idx = hdr.fileIndex ?: 0
        if (idx < 0 || idx >= manifest.size) {
            throw SessionException("file_index $idx out of range [0, ${manifest.size})")
        }
        val meta = manifest[idx]

        val result = runCatching { receiveOneFile(context, inp, meta, onProgress) }
        if (result.isSuccess) {
            received.add(result.getOrThrow())
            writeControl(out, Header(type = MsgType.ACK, fileIndex = idx, ok = true))
        } else {
            val msg = result.exceptionOrNull()?.message ?: "unknown error"
            writeControl(out, Header(type = MsgType.ACK, fileIndex = idx, ok = false, error = msg))
            throw SessionException("file ${meta.name}: $msg", result.exceptionOrNull())
        }
    }

    // Final session ack
    writeControl(out, Header(type = MsgType.ACK, ok = true))
    return SessionResult.Files(peerName, received)
}

private fun receiveOneFile(
    context: Context,
    inp: InputStream,
    meta: FileMeta,
    onProgress: FileProgressFn? = null,
): ReceivedFile {
    val digest = MessageDigest.getInstance("SHA-256")

    // If the sender set a relative path, use it as the display name so the
    // user can see the folder structure. MediaStore flattens Downloads into a
    // single directory, so the path becomes part of the filename.
    val displayName = if (!meta.relPath.isNullOrEmpty()) meta.relPath.replace('/', '_') else meta.name

    // Prepare MediaStore entry in Downloads.
    val values = ContentValues().apply {
        put(MediaStore.Downloads.DISPLAY_NAME, displayName)
        put(MediaStore.Downloads.MIME_TYPE, guessMime(meta.name))
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
    }

    val resolver = context.contentResolver
    val collection = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
    } else {
        MediaStore.Downloads.EXTERNAL_CONTENT_URI
    }
    val uri = resolver.insert(collection, values)
        ?: throw SessionException("MediaStore insert failed for ${meta.name}")

    try {
        resolver.openOutputStream(uri)!!.buffered().use { fileOut ->
            var got = 0L
            val buf = ByteArray(CHUNK_SIZE)
            while (true) {
                val hdr = readHeader(inp)
                when (hdr.type) {
                    MsgType.CHUNK -> {
                        val chunkLen = hdr.length ?: 0L
                        if (got + chunkLen > meta.size) {
                            throw SessionException("payload exceeds declared size for ${meta.name}")
                        }
                        var remaining = chunkLen
                        while (remaining > 0) {
                            val toRead = remaining.coerceAtMost(buf.size.toLong()).toInt()
                            val n = inp.read(buf, 0, toRead)
                            if (n < 0) throw SessionException("stream ended mid-chunk")
                            fileOut.write(buf, 0, n)
                            digest.update(buf, 0, n)
                            got += n
                            remaining -= n
                        }
                    }
                    MsgType.PROGRESS -> {
                        // Advisory frame from a newer sender; forward to caller.
                        val fi = hdr.fileIndex ?: 0
                        val done = hdr.bytesDone ?: got
                        val total = hdr.totalBytes ?: meta.size
                        onProgress?.invoke(fi, done, total)
                    }
                    MsgType.FILE_END -> {
                        fileOut.flush()
                        // Validate size
                        if (got != meta.size) {
                            throw SessionException(
                                "size mismatch for ${meta.name}: got $got want ${meta.size}"
                            )
                        }
                        // Validate SHA-256
                        val computedHex = digest.digest().joinToString("") { "%02x".format(it) }
                        if (!computedHex.equals(meta.sha256, ignoreCase = true)) {
                            throw SessionException("sha256 mismatch for ${meta.name}")
                        }
                        break
                    }
                    else -> throw SessionException("unexpected ${hdr.type} during file body")
                }
            }
        }

        // Mark as no longer pending so it's visible in other apps.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val done = ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }
            resolver.update(uri, done, null, null)
        }
        return ReceivedFile(displayName, uri)
    } catch (e: Exception) {
        // Delete the partial entry on any failure.
        resolver.delete(uri, null, null)
        throw e
    }
}

private fun receiveClipboard(
    inp: InputStream,
    out: OutputStream,
    peerName: String,
    onClipboard: (ByteArray, String) -> Unit,
): SessionResult {
    val hdr = readHeader(inp)
    if (hdr.type != MsgType.CLIPBOARD) {
        throw SessionException("expected clipboard, got ${hdr.type}")
    }
    val len = hdr.length ?: 0L
    val mime = hdr.mime ?: "text/plain"
    val buf = ByteArray(len.toInt())
    var offset = 0
    while (offset < buf.size) {
        val n = inp.read(buf, offset, buf.size - offset)
        if (n < 0) throw SessionException("stream ended reading clipboard payload")
        offset += n
    }

    // Consume session_end
    readHeader(inp)

    onClipboard(buf, mime)
    writeControl(out, Header(type = MsgType.ACK, ok = true))
    return SessionResult.Clipboard(peerName, buf, mime)
}

// ---------------------------------------------------------------------------
// Manifest builder
// ---------------------------------------------------------------------------

/**
 * Builds a [FileMeta] manifest by reading file data from [openFile].
 * Must be called on a worker thread (performs I/O + hashing).
 */
fun buildManifest(
    names: List<String>,
    sizes: List<Long>,
    openFile: (index: Int) -> InputStream,
): List<FileMeta> {
    require(names.size == sizes.size) { "names and sizes must be the same length" }
    return names.indices.map { i ->
        val digest = MessageDigest.getInstance("SHA-256")
        openFile(i).use { s ->
            val buf = ByteArray(CHUNK_SIZE)
            while (true) {
                val n = s.read(buf)
                if (n < 0) break
                digest.update(buf, 0, n)
            }
        }
        val sha256 = digest.digest().joinToString("") { "%02x".format(it) }
        FileMeta(name = names[i], size = sizes[i], sha256 = sha256)
    }
}

// ---------------------------------------------------------------------------
// Support types
// ---------------------------------------------------------------------------

sealed class SessionResult {
    data class Files(val peerName: String, val files: List<ReceivedFile>) : SessionResult()
    data class Clipboard(val peerName: String, val bytes: ByteArray, val mime: String) : SessionResult()
}

class SessionException(message: String, cause: Throwable? = null) :
    Exception(message, cause)

// Minimal MIME guessing by extension.
private fun guessMime(name: String): String {
    val ext = name.substringAfterLast('.', "").lowercase()
    return when (ext) {
        "pdf"  -> "application/pdf"
        "jpg", "jpeg" -> "image/jpeg"
        "png"  -> "image/png"
        "gif"  -> "image/gif"
        "mp4"  -> "video/mp4"
        "mp3"  -> "audio/mpeg"
        "txt"  -> "text/plain"
        "zip"  -> "application/zip"
        "apk"  -> "application/vnd.android.package-archive"
        else   -> "application/octet-stream"
    }
}
