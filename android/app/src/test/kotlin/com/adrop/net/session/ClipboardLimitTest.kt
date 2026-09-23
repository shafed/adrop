package com.adrop.net.session

import com.adrop.data.proto.Header
import com.adrop.data.proto.MAX_CLIPBOARD_SIZE
import com.adrop.data.proto.MsgType
import com.adrop.data.proto.writeControl
import com.adrop.data.proto.writeMessage
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer

/**
 * A clipboard frame's length comes from the peer and is allocated in one
 * piece, so it is checked before the allocation (mirrors Go's
 * TestHostileLengthsDoNotCrashReceiver). writeMessage refuses to frame a
 * length it has no payload for, so hostile frames are built by hand.
 */
class ClipboardLimitTest {

    private fun rawFrame(length: Long): ByteArray {
        val json = """{"type":"${MsgType.CLIPBOARD}","mime":"text/plain","length":$length}"""
            .toByteArray()
        return ByteBuffer.allocate(4 + json.size).putInt(json.size).put(json).array()
    }

    private fun receive(frame: ByteArray): ByteArray? {
        var got: ByteArray? = null
        receiveClipboard(ByteArrayInputStream(frame), ByteArrayOutputStream(), "peer") { b, _ -> got = b }
        return got
    }

    @Test
    fun `negative clipboard length is refused, not allocated`() {
        try {
            receive(rawFrame(-1))
            fail("negative length accepted")
        } catch (e: SessionException) {
            assertTrue(e.message, e.message!!.contains("limit"))
        }
    }

    @Test
    fun `clipboard length over the limit is refused before allocating`() {
        try {
            receive(rawFrame(MAX_CLIPBOARD_SIZE.toLong() + 1))
            fail("oversized length accepted")
        } catch (e: SessionException) {
            assertTrue(e.message, e.message!!.contains("limit"))
        }
    }

    @Test
    fun `ordinary clipboard still arrives`() {
        val out = ByteArrayOutputStream()
        val text = "привет".toByteArray()
        writeMessage(out, Header(type = MsgType.CLIPBOARD, mime = "text/plain", length = text.size.toLong()),
            text.inputStream())
        writeControl(out, Header(type = MsgType.SESSION_END))
        assertArrayEquals(text, receive(out.toByteArray()))
    }

    @Test
    fun `limit matches the Go side`() {
        assertEquals(32 shl 20, MAX_CLIPBOARD_SIZE)
    }
}
