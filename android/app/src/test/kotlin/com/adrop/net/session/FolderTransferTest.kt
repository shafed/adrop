package com.adrop.net.session

import com.adrop.data.proto.*
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayOutputStream

class FolderTransferTest {
    @Test fun `reject unsafe paths without normalizing them`() {
        for (path in listOf("", ".", "..", "../escape", "/absolute", "a/../b", "a//b", "C:/drive", "a\\b", "a/", "a\u0000b")) {
            assertTrue(path, runCatching { pathParts(path) }.isFailure)
        }
        assertEquals(listOf("папка", "dots..txt"), pathParts("папка/dots..txt"))
    }

    @Test fun `directory entries have no payload and retain empty nested folders`() = runBlocking {
        val replies = ByteArrayOutputStream()
        repeat(2) { i ->
            writeControl(replies, Header(type = MsgType.RESUME_OFFER, fileIndex = i))
            writeControl(replies, Header(type = MsgType.ACK, fileIndex = i, ok = true))
        }
        writeControl(replies, Header(type = MsgType.ACK, ok = true))
        val out = ByteArrayOutputStream()
        val entries = listOf("tree", "tree/empty").map {
            FileMeta(name = it.substringAfterLast('/'), size = 0, sha256 = "", relPath = it, isDir = true)
        }
        sendFiles(out, replies.toByteArray().inputStream(), entries, { error("Must not open directories") })
        val input = out.toByteArray().inputStream()
        assertEquals(entries, readHeader(input).files)
        repeat(2) { i ->
            assertEquals(MsgType.RESUME_QUERY, readHeader(input).type)
            assertEquals(Header(type = MsgType.FILE_HEADER, fileIndex = i), readHeader(input))
            assertEquals(Header(type = MsgType.FILE_END, fileIndex = i), readHeader(input))
        }
        assertEquals(MsgType.SESSION_END, readHeader(input).type)
        assertEquals(-1, input.read())
    }

    @Test fun `decode Go folder metadata and capability`() {
        val hello = protoJson.decodeFromString(Header.serializer(), """{"type":"hello","folders":true}""")
        assertTrue(hello.folders)
        assertFalse(protoJson.decodeFromString(Header.serializer(), """{"type":"hello"}""").folders)
        val entry = protoJson.decodeFromString(FileMeta.serializer(), """{"is_dir":true,"name":"empty","size":0,"sha256":"","rel_path":"tree/empty"}""")
        assertTrue(entry.isDir)
        assertEquals("tree/empty", entry.relPath)
    }
}
