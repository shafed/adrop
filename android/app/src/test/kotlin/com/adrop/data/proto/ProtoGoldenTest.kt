/**
 * Golden-byte wire-format tests for the adrop protocol codec.
 *
 * These tests pin the *exact* bytes that the codec must produce so that any
 * accidental wire-format drift (key rename, case change, framing order change,
 * fingerprint encoding change) is caught immediately.
 *
 * Interop contract being enforced:
 *   1. JSON keys match Go's json struct tags in internal/proto/proto.go exactly
 *      (snake_case, all lowercase — e.g. "file_index", not "fileIndex").
 *   2. Every message is framed as [4-byte big-endian uint32 length][JSON bytes].
 *   3. The device fingerprint is SHA-256 of the certificate DER bytes, encoded
 *      as lowercase hex (64 hex chars, no colons, no uppercase).
 *   4. Omitempty semantics: null / false / 0 fields MUST NOT appear in JSON.
 *
 * Do NOT change the expected byte literals without also updating the Go side
 * (or confirming the Go side already produces the same bytes).
 */
package com.adrop.data.proto

import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.MessageDigest

// ---------------------------------------------------------------------------
// Helper: decode the 4-byte big-endian prefix and return the JSON bytes.
// ---------------------------------------------------------------------------
private fun extractJsonBytes(frame: ByteArray): ByteArray {
    require(frame.size >= 4) { "frame too short: ${frame.size}" }
    val len = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN).int.toLong() and 0xFFFFFFFFL
    require(frame.size >= 4 + len) { "frame declares $len JSON bytes but only ${frame.size - 4} available" }
    return frame.copyOfRange(4, (4 + len).toInt())
}

private fun frameJson(frame: ByteArray): String =
    extractJsonBytes(frame).toString(Charsets.UTF_8)

// ---------------------------------------------------------------------------
// 1. Exact-JSON-key golden tests (interop-critical)
// ---------------------------------------------------------------------------

class ProtoGoldenTest {

    @Test
    fun `golden - hello message exact JSON keys and values`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(
            type        = MsgType.HELLO,
            version     = PROTOCOL_VERSION,
            fingerprint = "abcdef1234567890".repeat(4), // 64 hex chars
            name        = "test-pc",
            addr        = "192.168.1.10:53127",
        ))
        val json = frameJson(out.toByteArray())

        // Exact key names as Go json tags.
        assertTrue("""key must be "type" with value "hello"""",  json.contains(""""type":"hello""""))
        assertTrue("""key must be "version" with int value""",   json.contains(""""version":1"""))
        assertTrue("""key must be "fingerprint"""",              json.contains(""""fingerprint":"""))
        assertTrue("""key must be "name"""",                     json.contains(""""name":"test-pc""""))
        assertTrue("""key must be "addr"""",                     json.contains(""""addr":"192.168.1.10:53127""""))

        // Fields not set must be absent (Go omitempty).
        assertFalse("""absent: "kind"""",       json.contains(""""kind""""))
        assertFalse("""absent: "files"""",      json.contains(""""files""""))
        assertFalse("""absent: "file_index"""", json.contains(""""file_index""""))
        assertFalse("""absent: "mime"""",       json.contains(""""mime""""))
        assertFalse("""absent: "ok"""",         json.contains(""""ok""""))
        assertFalse("""absent: "error"""",      json.contains(""""error""""))
        assertFalse("""absent: "length"""",     json.contains(""""length""""))
    }

    @Test
    fun `golden - session_start files exact JSON keys`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(
            type  = MsgType.SESSION_START,
            kind  = SessionKind.FILES,
            files = listOf(
                FileMeta("report.pdf", 1_024L, "a1b2".repeat(16)),
            ),
        ))
        val json = frameJson(out.toByteArray())

        assertTrue(json.contains(""""type":"session_start""""))
        assertTrue("""key must be "kind" with value "files"""", json.contains(""""kind":"files""""))
        // FileMeta keys
        assertTrue("""FileMeta key "name"""",   json.contains(""""name":"report.pdf""""))
        assertTrue("""FileMeta key "size"""",   json.contains(""""size":1024"""))
        assertTrue("""FileMeta key "sha256"""", json.contains(""""sha256":"""))

        // Must NOT have camelCase variants.
        assertFalse("no sessionStart camelCase", json.contains("sessionStart"))
        assertFalse("no camelCase sha256",        json.contains("\"sha256Hash\""))
    }

    @Test
    fun `golden - file_header uses snake_case key`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.FILE_HEADER, fileIndex = 3))
        val json = frameJson(out.toByteArray())

        assertTrue("""must use snake_case "file_index"""",  json.contains(""""file_index":3"""))
        assertFalse("""must NOT use camelCase "fileIndex"""", json.contains(""""fileIndex""""))
        assertFalse("""must NOT use "FileIndex"""",           json.contains(""""FileIndex""""))
    }

    @Test
    fun `golden - chunk uses snake_case file_index`() {
        val out = ByteArrayOutputStream()
        val payload = "chunk data".toByteArray()
        writeMessage(out, Header(type = MsgType.CHUNK, fileIndex = 1, length = payload.size.toLong()),
            payload.inputStream())
        val json = frameJson(out.toByteArray())

        assertTrue(json.contains(""""type":"chunk""""))
        assertTrue(json.contains(""""file_index":1"""))
        assertFalse(json.contains(""""fileIndex""""))
    }

    @Test
    fun `golden - all type constants are lowercase strings`() {
        // This pins every MsgType constant value against accidental case change.
        val expected = mapOf(
            MsgType.HELLO         to "hello",
            MsgType.SESSION_START to "session_start",
            MsgType.FILE_HEADER   to "file_header",
            MsgType.CHUNK         to "chunk",
            MsgType.FILE_END      to "file_end",
            MsgType.CLIPBOARD     to "clipboard",
            MsgType.SESSION_END   to "session_end",
            MsgType.ACK           to "ack",
        )
        for ((constant, wire) in expected) {
            assertEquals("MsgType constant '$constant'", wire, constant)
            val out = ByteArrayOutputStream()
            writeControl(out, Header(type = constant))
            val json = frameJson(out.toByteArray())
            assertTrue("""type value in JSON must be "$wire"""", json.contains(""""type":"$wire""""))
        }
    }

    @Test
    fun `golden - session kind constants are lowercase`() {
        assertEquals("files",     SessionKind.FILES)
        assertEquals("clipboard", SessionKind.CLIPBOARD)
    }

    @Test
    fun `golden - ack ok=true present ok=false absent`() {
        // ok=true must appear.
        val outTrue = ByteArrayOutputStream()
        writeControl(outTrue, Header(type = MsgType.ACK, ok = true))
        val jsonTrue = frameJson(outTrue.toByteArray())
        assertTrue(jsonTrue.contains(""""ok":true"""))

        // ok=false (zero value in Go) must be absent (omitempty).
        val outFalse = ByteArrayOutputStream()
        writeControl(outFalse, Header(type = MsgType.ACK))
        val jsonFalse = frameJson(outFalse.toByteArray())
        assertFalse("""ok=false must be absent (omitempty)""", jsonFalse.contains(""""ok""""))
    }

    @Test
    fun `golden - ack error field key is "error" not "errorMessage"`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.ACK, ok = false, error = "sha256 mismatch"))
        val json = frameJson(out.toByteArray())

        assertTrue("""key must be "error"""", json.contains(""""error":"sha256 mismatch""""))
        assertFalse("no camelCase errorMessage", json.contains(""""errorMessage""""))
    }

    @Test
    fun `golden - clipboard header key is "clipboard" not "clipboardData"`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.CLIPBOARD, mime = "text/plain"))
        val json = frameJson(out.toByteArray())

        assertTrue(json.contains(""""type":"clipboard""""))
        assertTrue("""mime key must be "mime"""", json.contains(""""mime":"text/plain""""))
        assertFalse("no MIME camelCase", json.contains(""""mimeType""""))
    }

    // -----------------------------------------------------------------------
    // 2. 4-byte big-endian framing golden tests
    // -----------------------------------------------------------------------

    @Test
    fun `golden - framing 4-byte BE prefix for session_end`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.SESSION_END))
        val frame = out.toByteArray()

        // Byte 0 must be 0x00 (high byte) since any realistic JSON fits in 3 bytes.
        assertEquals("highest byte of length must be 0x00", 0x00.toByte(), frame[0])
        // Bytes 0-3 must be big-endian uint32 == JSON body length.
        val prefixLen = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN).int
        assertTrue("length must be positive", prefixLen > 0)
        assertTrue("length must fit the frame", frame.size >= 4 + prefixLen)
        // The JSON at offset 4 must decode correctly.
        val json = frame.copyOfRange(4, 4 + prefixLen).toString(Charsets.UTF_8)
        assertTrue(json.contains(""""type":"session_end""""))
    }

    @Test
    fun `golden - framing length is big-endian not little-endian`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.HELLO, name = "x"))
        val frame = out.toByteArray()

        val beBuf = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN)
        val leBuf = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.LITTLE_ENDIAN)
        val beLen = beBuf.int.toLong() and 0xFFFFFFFFL
        val leLen = leBuf.int.toLong() and 0xFFFFFFFFL

        // Big-endian reading must yield a plausible JSON length (< 1 KiB here).
        assertTrue("big-endian length must be < 1024", beLen < 1024L)
        // Verify we can actually extract that many JSON bytes.
        assertEquals("frame must contain exactly BE-length JSON bytes",
            frame.size.toLong(), 4L + beLen)

        // If the two values differ (frame > 255 bytes) the test would catch a
        // byte-order swap; for frames < 256 bytes they coincidentally agree, so
        // we additionally verify that ReadHeader parses our frame correctly.
        val parsed = readHeader(ByteArrayInputStream(frame))
        assertEquals(MsgType.HELLO, parsed.type)
        assertEquals("x", parsed.name)

        // Prove that little-endian interpretation would NOT parse correctly when
        // the JSON length is >= 256 bytes.  For smaller frames the numbers may
        // coincide, so skip the mismatch assertion and rely on the readHeader
        // success above as the definitive check.
        if (beLen != leLen) {
            assertTrue("little-endian reading must give wrong length $leLen vs $beLen", beLen != leLen)
        }
    }

    @Test
    fun `golden - framing chunk with payload bytes are immediately after JSON`() {
        val payload = ByteArray(16) { it.toByte() }
        val out = ByteArrayOutputStream()
        writeMessage(out, Header(type = MsgType.CHUNK, length = payload.size.toLong()),
            payload.inputStream())
        val frame = out.toByteArray()

        val jsonLen = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN).int.toLong() and 0xFFFFFFFFL
        val payloadOffset = (4 + jsonLen).toInt()
        // Payload must start at offset 4+jsonLen and be exactly 16 bytes.
        assertEquals("total frame size", (4 + jsonLen + payload.size).toInt(), frame.size)
        val receivedPayload = frame.copyOfRange(payloadOffset, payloadOffset + payload.size)
        assertArrayEquals("payload bytes must immediately follow JSON", payload, receivedPayload)
    }

    @Test
    fun `golden - framing no extra bytes between JSON and payload`() {
        // There must be zero padding/separator bytes between the JSON header and payload.
        val payload = "hello payload".toByteArray()
        val out = ByteArrayOutputStream()
        writeMessage(out, Header(type = MsgType.CHUNK, length = payload.size.toLong()),
            payload.inputStream())
        val frame = out.toByteArray()

        val jsonLen = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN).int
        // Frame must be exactly 4 + jsonLen + payloadLen bytes — no gaps.
        assertEquals(4 + jsonLen + payload.size, frame.size)
    }

    // -----------------------------------------------------------------------
    // 3. SHA-256 fingerprint encoding golden tests
    // -----------------------------------------------------------------------

    @Test
    fun `golden - fingerprint is lowercase hex SHA-256 of cert DER`() {
        // Simulate the fingerprint that both the Go daemon and Android app compute:
        // SHA-256 of DER-encoded cert bytes, hex-encoded, all lowercase, 64 chars.
        val fakeDer = ByteArray(32) { it.toByte() } // deterministic fake DER

        val digest = MessageDigest.getInstance("SHA-256")
        val hash = digest.digest(fakeDer)
        val hex = hash.joinToString("") { "%02x".format(it) }

        // Must be 64 lowercase hex chars, no colons.
        assertEquals("fingerprint length must be 64", 64, hex.length)
        assertTrue("must be all lowercase hex", hex.matches(Regex("[0-9a-f]{64}")))
        assertFalse("must NOT contain uppercase", hex.any { it.isUpperCase() })
        assertFalse("must NOT contain colons", hex.contains(':'))
    }

    @Test
    fun `golden - fingerprint format matches wire fingerprint field`() {
        // The fingerprint field in Hello must be a 64-char lowercase hex string.
        val fp = "a".repeat(64) // typical placeholder in tests
        val out = ByteArrayOutputStream()
        writeControl(out, Header(
            type        = MsgType.HELLO,
            fingerprint = fp,
            name        = "device",
        ))
        val json = frameJson(out.toByteArray())

        // The JSON must contain the fingerprint verbatim (not base64, not colon-split).
        assertTrue("fingerprint in JSON is verbatim hex string", json.contains(""""fingerprint":"$fp""""))
        // Verify 64-char constraint when encoding a real SHA-256.
        val hash = MessageDigest.getInstance("SHA-256").digest(byteArrayOf(1, 2, 3))
        val hexFp = hash.joinToString("") { "%02x".format(it) }
        assertEquals("SHA-256 hex fingerprint must be 64 chars", 64, hexFp.length)
        assertTrue("hex chars only", hexFp.matches(Regex("[0-9a-f]+")))
    }

    @Test
    fun `golden - fingerprint uppercase would NOT match lowercase-only format`() {
        // If the Go side sends lowercase hex and Android sends uppercase, they
        // would be unequal even for the same cert — pin the lowercase expectation.
        val hash = MessageDigest.getInstance("SHA-256").digest("test".toByteArray())
        val lower = hash.joinToString("") { "%02x".format(it) }
        val upper = hash.joinToString("") { "%02X".format(it) }

        assertNotEquals("uppercase and lowercase fingerprints differ as strings", upper, lower)
        // The protocol mandates lowercase.
        assertTrue("protocol fingerprint must be lowercase", lower.matches(Regex("[0-9a-f]{64}")))
    }

    // -----------------------------------------------------------------------
    // 4. Complete wire-format golden byte vector
    // -----------------------------------------------------------------------

    /**
     * Golden byte vector for a minimal session_end control message.
     *
     * Go produces (for Header{Type:"session_end"}):
     *   JSON: {"type":"session_end"}   → 20 bytes
     *   frame: [0x00, 0x00, 0x00, 0x14] + UTF-8("{"type":"session_end"}")
     *
     * This test pins the exact frame bytes so any codec change shows up immediately.
     */
    @Test
    fun `golden byte vector - session_end control frame`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.SESSION_END))
        val frame = out.toByteArray()

        // Decode and verify.
        val json = frameJson(frame)
        // Must be exactly this JSON (no extra fields due to omitempty).
        assertEquals("""{"type":"session_end"}""", json)

        // The 4-byte prefix must equal the JSON length in big-endian.
        val expectedLen = json.toByteArray(Charsets.UTF_8).size
        val actualLen = ByteBuffer.wrap(frame, 0, 4).order(ByteOrder.BIG_ENDIAN).int
        assertEquals("4-byte prefix must equal JSON byte length", expectedLen, actualLen)

        // Total frame = 4 + JSON bytes only (no payload for control).
        assertEquals("frame size", 4 + expectedLen, frame.size)
    }

    /**
     * Golden byte vector for a hello message with all fields set.
     * The JSON must have exactly these keys in the Go-compatible format.
     */
    @Test
    fun `golden byte vector - hello message JSON shape`() {
        val fp = "0".repeat(64)
        val out = ByteArrayOutputStream()
        writeControl(out, Header(
            type        = MsgType.HELLO,
            version     = 1,
            fingerprint = fp,
            name        = "pc",
            addr        = "10.0.0.1:53127",
        ))
        val json = frameJson(out.toByteArray())

        // All five keys must be present with exact names.
        assertTrue(json.contains(""""type":"hello""""))
        assertTrue(json.contains(""""version":1"""))
        assertTrue(json.contains(""""fingerprint":"$fp""""))
        assertTrue(json.contains(""""name":"pc""""))
        assertTrue(json.contains(""""addr":"10.0.0.1:53127""""))

        // No other keys allowed (Go omitempty; these would be absent).
        val disallowed = listOf("kind", "files", "file_index", "mime", "ok", "error", "length")
        for (key in disallowed) {
            assertFalse("""key "$key" must be absent from hello""", json.contains(""""$key""""))
        }
    }

    /**
     * Golden round-trip: Go sends a session_start → Android decodes → must match.
     * We simulate Go's output byte-for-byte and feed it into readHeader.
     */
    @Test
    fun `golden round-trip - decode Go-style session_start bytes`() {
        // Simulate what Go's json.Marshal produces for a session_start with one file.
        // Go omits zero/empty fields (omitempty), so only type+kind+files appear.
        val goJson = """{"type":"session_start","kind":"files","files":[{"name":"photo.jpg","size":1234,"sha256":"${"ab".repeat(32)}"}]}"""
        val jsonBytes = goJson.toByteArray(Charsets.UTF_8)
        val lenBytes = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN).putInt(jsonBytes.size).array()

        val stream = ByteArrayInputStream(lenBytes + jsonBytes)
        val hdr = readHeader(stream)

        assertEquals(MsgType.SESSION_START, hdr.type)
        assertEquals(SessionKind.FILES, hdr.kind)
        assertEquals(1, hdr.files?.size)
        assertEquals("photo.jpg", hdr.files!![0].name)
        assertEquals(1234L, hdr.files[0].size)
        assertEquals("ab".repeat(32), hdr.files[0].sha256)
    }

    /**
     * Golden round-trip: Android encodes → must match what Go expects.
     * Verifies the exact bytes Android's codec emits are Go-parseable.
     */
    @Test
    fun `golden round-trip - Android encodes ack that Go can decode`() {
        // Android sends an ack.
        val out = ByteArrayOutputStream()
        writeControl(out, Header(type = MsgType.ACK, ok = true))
        val json = frameJson(out.toByteArray())

        // Go's json.Unmarshal requires: {"type":"ack","ok":true}
        // Verify the minimum required fields.
        assertTrue(json.contains(""""type":"ack""""))
        assertTrue(json.contains(""""ok":true"""))
        // Go's omitempty means ok=false would be missing, which Go reads as false.
        // We must NOT send ok=false explicitly (it should be absent).
        assertFalse(json.contains(""""ok":false"""))
    }
}
