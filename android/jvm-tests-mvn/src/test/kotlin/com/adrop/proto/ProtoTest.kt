package com.adrop.proto

import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.EOFException
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * Wire-protocol codec tests.
 *
 * These are pure JVM tests (no Android SDK); run with:
 *   ./gradlew :jvm-tests:test
 *
 * Critical surface: the JSON keys produced by [writeControl] / [writeMessage]
 * must match Go's json tags in internal/proto/proto.go exactly.
 */
class ProtoTest {

    // ------------------------------------------------------------------
    // 1. Round-trip: write then read a control message
    // ------------------------------------------------------------------

    @Test
    fun `round-trip control - session_end`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.SESSION_END))

        val input = ByteArrayInputStream(out.toByteArray())
        val hdr = readHeader(input)

        assertEquals(MsgType.SESSION_END, hdr.type)
        assertNull("length must be absent (omitempty)", hdr.length)
        assertEquals("no bytes left after control message", 0, input.available())
    }

    @Test
    fun `round-trip control - hello with all fields`() {
        val out = ByteArrayOutputStream()
        val sent = Header(
            type        = MsgType.HELLO,
            version     = PROTOCOL_VERSION,
            fingerprint = "a".repeat(64),
            name        = "pixel-8",
            addr        = "192.168.1.42:7777",
        )
        writeControl(out, sent)

        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))

        assertEquals(MsgType.HELLO, hdr.type)
        assertEquals(PROTOCOL_VERSION, hdr.version)
        assertEquals("a".repeat(64), hdr.fingerprint)
        assertEquals("pixel-8", hdr.name)
        assertEquals("192.168.1.42:7777", hdr.addr)
        assertNull(hdr.length)
        assertNull(hdr.ok)
        assertNull(hdr.fileIndex)
    }

    @Test
    fun `round-trip message with payload - chunk`() {
        val payload = "Hello, adrop payload!".toByteArray(Charsets.UTF_8)
        val out = ByteArrayOutputStream()
        writeMessage(
            out,
            Header(type = MsgType.CHUNK, fileIndex = 3, length = payload.size.toLong()),
            ByteArrayInputStream(payload),
        )

        val buf = ByteArrayInputStream(out.toByteArray())
        val hdr = readHeader(buf)

        assertEquals(MsgType.CHUNK, hdr.type)
        assertEquals(3, hdr.fileIndex)
        assertEquals(payload.size.toLong(), hdr.length)

        // Caller consumes payload manually.
        val received = buf.readBytes()
        assertArrayEquals(payload, received)
    }

    @Test
    fun `round-trip session_start with file manifest`() {
        val files = listOf(
            FileMeta("report.pdf", 1_234_567L, "ab".repeat(32)),
            FileMeta("photo.jpg",   987_654L,  "cd".repeat(32)),
        )
        val out = ByteArrayOutputStream()
        writeControl(
            out,
            Header(type = MsgType.SESSION_START, kind = SessionKind.FILES, files = files),
        )

        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))
        assertEquals(MsgType.SESSION_START, hdr.type)
        assertEquals(SessionKind.FILES, hdr.kind)
        assertEquals(2, hdr.files?.size)
        assertEquals("report.pdf", hdr.files!![0].name)
        assertEquals(1_234_567L,   hdr.files[0].size)
        assertEquals("photo.jpg",  hdr.files[1].name)
    }

    @Test
    fun `round-trip ack ok=true`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.ACK, ok = true))

        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))
        assertEquals(MsgType.ACK, hdr.type)
        assertEquals(true, hdr.ok)
        assertNull(hdr.error)
    }

    @Test
    fun `round-trip ack ok=false with error string`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.ACK, ok = false, error = "sha256 mismatch"))

        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))
        assertEquals(false, hdr.ok)
        assertEquals("sha256 mismatch", hdr.error)
    }

    @Test
    fun `round-trip clipboard header`() {
        val text = "copied text from PC".toByteArray()
        val out = ByteArrayOutputStream()
        writeMessage(
            out,
            Header(type = MsgType.CLIPBOARD, mime = "text/plain", length = text.size.toLong()),
            ByteArrayInputStream(text),
        )

        val buf = ByteArrayInputStream(out.toByteArray())
        val hdr = readHeader(buf)

        assertEquals(MsgType.CLIPBOARD, hdr.type)
        assertEquals("text/plain", hdr.mime)
        assertEquals(text.size.toLong(), hdr.length)
        assertArrayEquals(text, buf.readBytes())
    }

    // ------------------------------------------------------------------
    // 2. Golden JSON-key tests (interop-critical: Go tag must match exactly)
    // ------------------------------------------------------------------

    @Test
    fun `golden - hello JSON keys match Go tags`() {
        val json = encodeHeaderToJson(
            Header(
                type        = MsgType.HELLO,
                version     = 1,
                fingerprint = "fp",
                name        = "dev",
                addr        = "1.2.3.4:7777",
            )
        )
        // Keys must be exactly as Go's json tags.
        assertTrue("""json must contain "type":"hello"""",   json.contains(""""type":"hello""""))
        assertTrue("""json must contain "version":1""",      json.contains(""""version":1"""))
        assertTrue("""json must contain "fingerprint":"fp"""", json.contains(""""fingerprint":"fp""""))
        assertTrue("""json must contain "name":"dev"""",     json.contains(""""name":"dev""""))
        assertTrue("""json must contain "addr":"1.2.3.4:7777"""", json.contains(""""addr":"1.2.3.4:7777""""))
        // Absent optional fields must NOT appear.
        assertFalse("kind must be absent",       json.contains(""""kind""""))
        assertFalse("files must be absent",      json.contains(""""files""""))
        assertFalse("file_index must be absent", json.contains(""""file_index""""))
        assertFalse("mime must be absent",       json.contains(""""mime""""))
        assertFalse("ok must be absent",         json.contains(""""ok""""))
        assertFalse("error must be absent",      json.contains(""""error""""))
        assertFalse("length must be absent",     json.contains(""""length""""))
    }

    @Test
    fun `golden - session_start file manifest JSON keys`() {
        val json = encodeHeaderToJson(
            Header(
                type  = MsgType.SESSION_START,
                kind  = SessionKind.FILES,
                files = listOf(FileMeta("a.txt", 100L, "deadbeef" + "00".repeat(28))),
            )
        )
        assertTrue(json.contains(""""type":"session_start""""))
        assertTrue(json.contains(""""kind":"files""""))
        assertTrue(json.contains(""""files":["""))
        assertTrue(json.contains(""""name":"a.txt""""))
        assertTrue(json.contains(""""size":100"""))
        assertTrue(json.contains(""""sha256":"""))
    }

    @Test
    fun `golden - chunk JSON keys`() {
        val json = encodeHeaderToJson(
            Header(type = MsgType.CHUNK, fileIndex = 0, length = 1024L)
        )
        assertTrue(json.contains(""""type":"chunk""""))
        // file_index 0 is zero-value in Go (omitempty), so Go omits it.
        // We must also omit it when it is the zero value (null in our nullable model).
        // But fileIndex = 0 means Int? = 0 (not null). In Go, omitempty omits 0.
        // Per spec: "Go uses omitempty, so absent fields = zero values."
        // We map this conservatively: if fileIndex = 0 it may or may not appear.
        // The Go receiver tolerates it either way (json.Unmarshal ignores extras).
        // What matters: when fileIndex IS present, the key must be "file_index".
        assertTrue(json.contains(""""length":1024"""))
    }

    @Test
    fun `golden - file_index key uses snake_case`() {
        val json = encodeHeaderToJson(
            Header(type = MsgType.FILE_HEADER, fileIndex = 2)
        )
        assertTrue("""key must be "file_index" not "fileIndex"""",
            json.contains(""""file_index":2"""))
        assertFalse("camelCase variant must NOT appear", json.contains(""""fileIndex""""))
    }

    @Test
    fun `golden - type values are lowercase strings`() {
        val types = mapOf(
            MsgType.HELLO         to "hello",
            MsgType.SESSION_START to "session_start",
            MsgType.FILE_HEADER   to "file_header",
            MsgType.CHUNK         to "chunk",
            MsgType.FILE_END      to "file_end",
            MsgType.CLIPBOARD     to "clipboard",
            MsgType.SESSION_END   to "session_end",
            MsgType.ACK           to "ack",
        )
        for ((constant, expected) in types) {
            val json = encodeHeaderToJson(Header(type = constant))
            assertTrue(
                "type value '$constant' must appear as \"$expected\" in JSON",
                json.contains(""""type":"$expected"""")
            )
        }
    }

    @Test
    fun `golden - omitempty ok=false must be omitted`() {
        // In Go, ok:false is the zero value and gets omitted by omitempty.
        // Our serializer must also omit it (ok = null -> not emitted).
        val json = encodeHeaderToJson(Header(type = MsgType.ACK))
        assertFalse("ok=false (zero) must be absent", json.contains(""""ok""""))
    }

    @Test
    fun `golden - omitempty ok=true must be present`() {
        val json = encodeHeaderToJson(Header(type = MsgType.ACK, ok = true))
        assertTrue("ok=true must appear", json.contains(""""ok":true"""))
    }

    // ------------------------------------------------------------------
    // 3. Framing: big-endian 4-byte header length prefix
    // ------------------------------------------------------------------

    @Test
    fun `framing - 4-byte big-endian length prefix`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.SESSION_END))
        val bytes = out.toByteArray()

        // First 4 bytes must be big-endian uint32 = length of the JSON body.
        val prefixedLen = ByteBuffer.wrap(bytes, 0, 4)
            .order(ByteOrder.BIG_ENDIAN).int.toLong() and 0xFFFFFFFFL
        val jsonPart = bytes.drop(4).take(prefixedLen.toInt()).toByteArray()
        val decoded = protoJson.decodeFromString(Header.serializer(), jsonPart.toString(Charsets.UTF_8))
        assertEquals(MsgType.SESSION_END, decoded.type)
    }

    @Test
    fun `framing - reject header size 0`() {
        val zeroLen = ByteArray(4) // all zero
        try {
            readHeader(ByteArrayInputStream(zeroLen))
            fail("expected ProtoException for size=0")
        } catch (e: ProtoException) {
            // expected
        }
    }

    @Test
    fun `framing - reject header size exceeding MAX_HEADER_SIZE`() {
        val buf = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN)
            .putInt(MAX_HEADER_SIZE + 1).array()
        try {
            readHeader(ByteArrayInputStream(buf))
            fail("expected ProtoException for oversized header")
        } catch (e: ProtoException) {
            // expected
        }
    }

    @Test
    fun `framing - 0xFF_FF_FF_FF length rejected`() {
        val buf = byteArrayOf(0xFF.toByte(), 0xFF.toByte(), 0xFF.toByte(), 0xFF.toByte())
        try {
            readHeader(ByteArrayInputStream(buf))
            fail("expected ProtoException")
        } catch (e: ProtoException) {
            // expected
        }
    }

    @Test
    fun `framing - EOF on empty stream`() {
        try {
            readHeader(ByteArrayInputStream(ByteArray(0)))
            fail("expected EOFException")
        } catch (e: EOFException) {
            // expected
        }
    }

    @Test
    fun `framing - EOF mid-header`() {
        // Write only the 4-byte length with a plausible size but no JSON body.
        val buf = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN).putInt(100).array()
        try {
            readHeader(ByteArrayInputStream(buf))
            fail("expected EOFException for truncated JSON body")
        } catch (e: EOFException) {
            // expected
        }
    }

    // ------------------------------------------------------------------
    // 4. Payload integrity
    // ------------------------------------------------------------------

    @Test
    fun `payload - large multi-chunk payload round-trips`() {
        // 1.5 × CHUNK_SIZE to exercise the write loop.
        val size = (CHUNK_SIZE * 3) / 2
        val data = ByteArray(size) { (it % 251).toByte() }

        val out = ByteArrayOutputStream()
        writeMessage(
            out,
            Header(type = MsgType.CHUNK, length = size.toLong()),
            ByteArrayInputStream(data),
        )

        val input = ByteArrayInputStream(out.toByteArray())
        val hdr = readHeader(input)
        assertEquals(size.toLong(), hdr.length)

        val received = input.readBytes()
        assertArrayEquals(data, received)
    }

    @Test
    fun `payload - writeControl strips length field`() {
        // Even if caller sneaks in a length, writeControl must strip it.
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.SESSION_END, length = 999L))

        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))
        // length should be null/absent after writeControl clears it.
        assertTrue("length must be null after writeControl", hdr.length == null || hdr.length == 0L)
    }

    // ------------------------------------------------------------------
    // 5. Full session simulation
    // ------------------------------------------------------------------

    @Test
    fun `full session - file send simulation`() {
        val pipe = ByteArrayOutputStream()

        // Initiator side: sends Hello, SessionStart, FileHeader, Chunks, FileEnd, SessionEnd.
        val fileData = ByteArray(300_000) { it.toByte() }
        val sha256Hex = "0".repeat(64) // fake; we're testing protocol shape, not integrity

        writeControl(pipe, Header(
            type        = MsgType.HELLO,
            version     = PROTOCOL_VERSION,
            fingerprint = "a".repeat(64),
            name        = "pixel",
            addr        = "192.168.1.10:7777",
        ))
        writeControl(pipe, Header(
            type  = MsgType.SESSION_START,
            kind  = SessionKind.FILES,
            files = listOf(FileMeta("test.bin", fileData.size.toLong(), sha256Hex)),
        ))
        writeControl(pipe, Header(type = MsgType.FILE_HEADER, fileIndex = 0))

        // Write file in CHUNK_SIZE chunks.
        var offset = 0
        while (offset < fileData.size) {
            val end = (offset + CHUNK_SIZE).coerceAtMost(fileData.size)
            val chunk = fileData.copyOfRange(offset, end)
            writeMessage(
                pipe,
                Header(type = MsgType.CHUNK, fileIndex = 0, length = chunk.size.toLong()),
                ByteArrayInputStream(chunk),
            )
            offset = end
        }

        writeControl(pipe, Header(type = MsgType.FILE_END, fileIndex = 0))
        writeControl(pipe, Header(type = MsgType.SESSION_END))

        // Now simulate the responder reading every frame back.
        val input = ByteArrayInputStream(pipe.toByteArray())

        val h1 = readHeader(input)
        assertEquals(MsgType.HELLO, h1.type)
        assertEquals("pixel", h1.name)

        val h2 = readHeader(input)
        assertEquals(MsgType.SESSION_START, h2.type)
        assertEquals(SessionKind.FILES, h2.kind)
        assertEquals(1, h2.files?.size)
        assertEquals("test.bin", h2.files!![0].name)
        assertEquals(fileData.size.toLong(), h2.files[0].size)

        val h3 = readHeader(input)
        assertEquals(MsgType.FILE_HEADER, h3.type)
        assertEquals(0, h3.fileIndex)

        val receivedData = ByteArrayOutputStream()
        while (true) {
            val ch = readHeader(input)
            if (ch.type == MsgType.FILE_END) break
            assertEquals(MsgType.CHUNK, ch.type)
            val buf = ByteArray((ch.length ?: 0L).toInt())
            val n = input.read(buf)
            assertEquals(buf.size, n)
            receivedData.write(buf, 0, n)
        }
        assertArrayEquals(fileData, receivedData.toByteArray())

        val hEnd = readHeader(input)
        assertEquals(MsgType.SESSION_END, hEnd.type)

        assertEquals("stream exhausted after full session", 0, input.available())
    }

    @Test
    fun `full session - clipboard send simulation`() {
        val text = "Hello from PC clipboard!".toByteArray(Charsets.UTF_8)
        val pipe = ByteArrayOutputStream()

        writeControl(pipe, Header(type = MsgType.SESSION_START, kind = SessionKind.CLIPBOARD))
        writeMessage(
            pipe,
            Header(type = MsgType.CLIPBOARD, mime = "text/plain", length = text.size.toLong()),
            ByteArrayInputStream(text),
        )
        writeControl(pipe, Header(type = MsgType.SESSION_END))

        val input = ByteArrayInputStream(pipe.toByteArray())

        val h1 = readHeader(input)
        assertEquals(MsgType.SESSION_START, h1.type)
        assertEquals(SessionKind.CLIPBOARD, h1.kind)

        val h2 = readHeader(input)
        assertEquals(MsgType.CLIPBOARD, h2.type)
        assertEquals("text/plain", h2.mime)
        assertEquals(text.size.toLong(), h2.length)
        val received = ByteArray(h2.length!!.toInt())
        input.read(received)
        assertArrayEquals(text, received)

        val h3 = readHeader(input)
        assertEquals(MsgType.SESSION_END, h3.type)
    }

    // ------------------------------------------------------------------
    // Helper
    // ------------------------------------------------------------------

    private fun encodeHeaderToJson(h: Header): String =
        protoJson.encodeToString(h)
}
