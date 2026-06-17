/**
 * adrop wire-protocol codec — pure Kotlin/JVM, zero Android dependencies.
 *
 * Framing (big-endian):
 *   [4-byte uint32: JSON header length][JSON bytes][raw payload bytes]
 *
 * The Header.length field gives the number of raw payload bytes that follow
 * the JSON. Control messages have length == 0 and no payload.
 *
 * JSON keys MUST match the Go json tags in internal/proto/proto.go exactly.
 * Go uses omitempty on every optional field; we reflect that with nullable
 * fields and encodeDefaults = false.
 */
package com.adrop.data.proto

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
const val MAX_HEADER_SIZE = 1 shl 20

/** File payload per Chunk message: 256 KiB. */
const val CHUNK_SIZE = 256 * 1024

// ---------------------------------------------------------------------------
// Message type constants (match Go's TypeXxx consts exactly)
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
    /**
     * Advisory per-file transfer progress emitted by the sender after each
     * chunk. Old peers that receive this frame from a newer sender should
     * ignore it (they will see an unknown [type] and skip it). Senders must
     * use [writeControl] so no payload bytes follow this header, which ensures
     * receivers can safely skip it without consuming extra bytes.
     */
    const val PROGRESS      = "progress"

    /** Sent before FILE_HEADER when session Resume=true; receiver checks for a partial file. */
    const val RESUME_QUERY  = "resume_query"

    /** Receiver's reply to RESUME_QUERY: bytes_done=N means N bytes already received. */
    const val RESUME_OFFER  = "resume_offer"
}

object SessionKind {
    const val FILES     = "files"
    const val CLIPBOARD = "clipboard"
}

// ---------------------------------------------------------------------------
// FileMeta
// ---------------------------------------------------------------------------

@Serializable
data class FileMeta(
    @SerialName("name")     val name:    String,
    @SerialName("size")     val size:    Long,
    @SerialName("sha256")   val sha256:  String,
    @SerialName("rel_path") val relPath: String? = null,
)

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

@Serializable
data class Header(
    @SerialName("type")        val type:        String,
    @SerialName("version")     val version:     Int?            = null,
    @SerialName("fingerprint") val fingerprint: String?         = null,
    @SerialName("name")        val name:        String?         = null,
    @SerialName("addr")        val addr:        String?         = null,
    @SerialName("kind")        val kind:        String?         = null,
    @SerialName("files")       val files:       List<FileMeta>? = null,
    @SerialName("file_index")  val fileIndex:   Int?            = null,
    @SerialName("mime")        val mime:        String?         = null,
    @SerialName("ok")          val ok:          Boolean?        = null,
    @SerialName("error")       val error:       String?         = null,
    @SerialName("length")      val length:      Long?           = null,
    // Progress / resume fields — absent in message types that don't use them (omitempty).
    @SerialName("bytes_done")  val bytesDone:   Long?           = null,
    @SerialName("total_bytes") val totalBytes:  Long?           = null,
    // Resume handshake fields.
    @SerialName("resume")      val resume:      Boolean?        = null,
    @SerialName("sha256")      val sha256:      String?         = null,
)

// ---------------------------------------------------------------------------
// JSON configuration
// ---------------------------------------------------------------------------

val protoJson: Json = Json {
    encodeDefaults    = false   // null fields omitted (matches Go omitempty)
    explicitNulls     = false
    ignoreUnknownKeys = true    // forward-compat with newer Go daemon
}

// ---------------------------------------------------------------------------
// Frame writer
// ---------------------------------------------------------------------------

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
            if (n < 0) throw EOFException("payload stream ended prematurely")
            out.write(buf, 0, n)
            remaining -= n
        }
    }
    out.flush()
}

@Throws(ProtoException::class)
fun writeControl(out: OutputStream, header: Header) {
    writeMessage(out, header.copy(length = null), null)
}

// ---------------------------------------------------------------------------
// Frame reader
// ---------------------------------------------------------------------------

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
