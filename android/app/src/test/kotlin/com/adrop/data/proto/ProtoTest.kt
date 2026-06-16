/**
 * JVM unit tests for the app's proto codec.
 * (Same logic as jvm-tests module; kept here for Android Studio's test runner.)
 */
package com.adrop.data.proto

import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.EOFException
import java.nio.ByteBuffer
import java.nio.ByteOrder

class ProtoTest {

    @Test
    fun `round-trip hello control message`() {
        val out = ByteArrayOutputStream()
        writeControl(out, Header(
            type        = MsgType.HELLO,
            version     = PROTOCOL_VERSION,
            fingerprint = "a".repeat(64),
            name        = "test-device",
            addr        = "192.168.1.1:7777",
        ))
        val hdr = readHeader(ByteArrayInputStream(out.toByteArray()))
        assertEquals(MsgType.HELLO, hdr.type)
        assertEquals("test-device", hdr.name)
        assertNull(hdr.length)
    }

    @Test
    fun `round-trip chunk with payload`() {
        val data = "test payload".toByteArray()
        val out  = ByteArrayOutputStream()
        writeMessage(out, Header(type = MsgType.CHUNK, fileIndex = 1, length = data.size.toLong()),
            data.inputStream())
        val buf = ByteArrayInputStream(out.toByteArray())
        val hdr = readHeader(buf)
        assertEquals(MsgType.CHUNK, hdr.type)
        assertEquals(1, hdr.fileIndex)
        assertEquals(data.size.toLong(), hdr.length)
        assertArrayEquals(data, buf.readBytes())
    }

    @Test
    fun `golden JSON keys`() {
        val json = protoJson.encodeToString(
            Header(type = MsgType.FILE_HEADER, fileIndex = 2)
        )
        assertTrue(json.contains(""""file_index":2"""))
        assertFalse(json.contains(""""fileIndex""""))
    }

    @Test
    fun `reject zero header size`() {
        try {
            readHeader(ByteArrayInputStream(ByteArray(4)))
            fail("expected ProtoException")
        } catch (e: ProtoException) { /* expected */ }
    }

    @Test
    fun `reject oversized header`() {
        val buf = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN)
            .putInt(MAX_HEADER_SIZE + 1).array()
        try {
            readHeader(ByteArrayInputStream(buf))
            fail("expected ProtoException")
        } catch (e: ProtoException) { /* expected */ }
    }

    @Test
    fun `EOF on empty stream`() {
        try {
            readHeader(ByteArrayInputStream(ByteArray(0)))
            fail("expected EOFException")
        } catch (e: EOFException) { /* expected */ }
    }
}
