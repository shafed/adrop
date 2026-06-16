/**
 * adrop wire-protocol codec — pure Kotlin/JVM, zero Android dependencies.
 *
 * Framing (big-endian):
 *   [4-byte uint32: JSON header length][JSON bytes][raw payload bytes]
 *
 * The Header.length field gives the number of raw payload bytes that follow
 * the JSON. Control messages have length == 0 and no payload.
 *
 * JSON keys MUST match the Go json tags exactly (camelCase where Go uses it,
 * snake_case where Go uses it). The Go source uses omitempty on every optional
 * field, so we also omit zero/empty values.
 *
 * Reference: internal/proto/proto.go in the Go daemon.
 */
package com.adrop.proto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.EOFException
import java.io.InputStream
import java.io.OutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const val PROTOCOL_VERSION = 1

/** 1 MiB — guards against hostile/corrupt peers allocating huge buffers. */
const val MAX_HEADER_SIZE = 1 shl 20 // 1_048_576

/** File payload per Chunk message: 256 KiB. */
const val CHUNK_SIZE = 256 * 1024

// ---------------------------------------------------------------------------
// Message type constants  (match Go's const block exactly)
// ---------------------------------------------------------------------------

object MsgType {
    const val HELLO         = "hello"
    const val SESSION_START = "session_start"
    const val FILE_HEADER   = "file_header"
    const val CHUNK         = "chunk"
    const val FILE_END      = "file_end"
    const val CLIPBOARD     = "clipboard"
    const val SESSION_END   = "session_end"
    const val ACK           = "ack"
}

object SessionKind {
    const val FILES     = "files"
    const val CLIPBOARD = "clipboard"
}

// ---------------------------------------------------------------------------
// FileMeta
// ---------------------------------------------------------------------------

/**
 * Describes one file in a session manifest.
 * JSON keys: "name", "size", "sha256" — these are NOT omitempty in Go
 * (they're always present in a manifest entry).
 */
@Serializable
data class FileMeta(
    @SerialName("name")   val name:   String,
    @SerialName("size")   val size:   Long,
    @SerialName("sha256") val sha256: String,
)

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

/**
 * Every framed message has exactly one Header.
 *
 * IMPORTANT: only non-zero / non-empty fields must appear in the serialized
 * JSON (Go's omitempty). We achieve this by making every optional field
 * nullable and using a custom Json instance that skips nulls.
 *
 * Go JSON tags (verbatim):
 *   type        string
 *   version     int,omitempty
 *   fingerprint string,omitempty
 *   name        string,omitempty
 *   addr        string,omitempty
 *   kind        string,omitempty
 *   files       []FileMeta,omitempty
 *   file_index  int,omitempty
 *   mime        string,omitempty
 *   ok          bool,omitempty
 *   error       string,omitempty
 *   length      int64,omitempty
 */
@Serializable
data class Header(
    @SerialName("type")        val type:        String,
    @SerialName("version")     val version:     Int?       = null,
    @SerialName("fingerprint") val fingerprint: String?    = null,
    @SerialName("name")        val name:        String?    = null,
    @SerialName("addr")        val addr:        String?    = null,
    @SerialName("kind")        val kind:        String?    = null,
    @SerialName("files")       val files:       List<FileMeta>? = null,
    @SerialName("file_index")  val fileIndex:   Int?       = null,
    @SerialName("mime")        val mime:        String?    = null,
    @SerialName("ok")          val ok:          Boolean?   = null,
    @SerialName("error")       val error:       String?    = null,
    @SerialName("length")      val length:      Long?      = null,
)

// ---------------------------------------------------------------------------
// JSON configuration
// ---------------------------------------------------------------------------

/**
 * Shared Json instance.
 *
 * encodeDefaults = false  ->  null fields are omitted (matches Go omitempty).
 * explicitNulls = false   ->  same effect for nullable fields.
 * ignoreUnknownKeys = true -> future-proof against new fields from the Go side.
 */
val protoJson: Json = Json {
    encodeDefaults  = false
    explicitNulls   = false
    ignoreUnknownKeys = true
}

// ---------------------------------------------------------------------------
// Frame writer
// ---------------------------------------------------------------------------

/**
 * Writes a complete framed message:
 *   [4-byte BE header length][JSON header bytes][payload bytes]
 *
 * If [header.length] > 0, exactly that many bytes are consumed from [payload].
 * If [header.length] is null / 0, no payload is written.
 *
 * @throws IllegalArgumentException if header.length > 0 but payload is null.
 * @throws ProtoException           if encoded header exceeds MAX_HEADER_SIZE.
 */
@Throws(ProtoException::class)
fun writeMessage(out: OutputStream, header: Header, payload: InputStream? = null) {
    val jsonBytes = protoJson.encodeToString(header).toByteArray(Charsets.UTF_8)
    if (jsonBytes.size > MAX_HEADER_SIZE) {
        throw ProtoException("header too large: ${jsonBytes.size} bytes")
    }
    val lenBuf = ByteBuffer.allocate(4)
        .order(ByteOrder.BIG_ENDIAN)
        .putInt(jsonBytes.size)
        .array()

    out.write(lenBuf)
    out.write(jsonBytes)

    val payloadLen = header.length ?: 0L
    if (payloadLen > 0L) {
        requireNotNull(payload) {
            "header declares $payloadLen payload bytes but payload stream is null"
        }
        val buf = ByteArray(CHUNK_SIZE.coerceAtMost(payloadLen.toInt().coerceAtLeast(4096)))
        var remaining = payloadLen
        while (remaining > 0L) {
            val toRead = remaining.coerceAtMost(buf.size.toLong()).toInt()
            val n = payload.read(buf, 0, toRead)
            if (n < 0) throw EOFException("payload stream ended after ${payloadLen - remaining} of $payloadLen bytes")
            out.write(buf, 0, n)
            remaining -= n
        }
    }
    out.flush()
}

/**
 * Convenience: writes a control message (length = 0, no payload).
 */
@Throws(ProtoException::class)
fun writeControl(out: OutputStream, header: Header) {
    // Strip any stray length so we don't accidentally ask for payload.
    writeMessage(out, header.copy(length = null), null)
}

// ---------------------------------------------------------------------------
// Frame reader
// ---------------------------------------------------------------------------

/**
 * Reads and decodes the next message header.
 *
 * After this call the caller MUST consume exactly [Header.length] bytes from
 * the same [InputStream] before calling [readHeader] again.
 *
 * @throws ProtoException   if the header size field is 0 or > MAX_HEADER_SIZE.
 * @throws EOFException     if the stream ends mid-frame.
 */
@Throws(ProtoException::class, EOFException::class)
fun readHeader(input: InputStream): Header {
    val lenBuf = ByteArray(4)
    readFully(input, lenBuf)

    val n = ByteBuffer.wrap(lenBuf).order(ByteOrder.BIG_ENDIAN).int.toLong() and 0xFFFFFFFFL
    if (n == 0L || n > MAX_HEADER_SIZE) {
        throw ProtoException("invalid header size: $n")
    }
    val raw = ByteArray(n.toInt())
    readFully(input, raw)

    return try {
        protoJson.decodeFromString(Header.serializer(), raw.toString(Charsets.UTF_8))
    } catch (e: Exception) {
        throw ProtoException("failed to decode header: ${e.message}", e)
    }
}

/**
 * Reads exactly [buf.size] bytes from [input] into [buf], throwing
 * [EOFException] on premature end-of-stream.
 */
private fun readFully(input: InputStream, buf: ByteArray) {
    var offset = 0
    while (offset < buf.size) {
        val n = input.read(buf, offset, buf.size - offset)
        if (n < 0) throw EOFException("stream ended at offset $offset of ${buf.size}")
        offset += n
    }
}

// ---------------------------------------------------------------------------
// Exceptions
// ---------------------------------------------------------------------------

class ProtoException(message: String, cause: Throwable? = null) :
    Exception(message, cause)
