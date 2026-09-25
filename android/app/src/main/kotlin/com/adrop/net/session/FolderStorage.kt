package com.adrop.net.session

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract as Docs
import java.io.File

internal fun pathParts(path: String): List<String> {
    val parts = path.split('/')
    require(parts.all { it.isNotEmpty() && it != "." && it != ".." && it.none { c -> c == '\\' || c == ':' || c == '\u0000' } }) {
        "Unsafe relative path: $path"
    }
    return parts
}

object FolderStorage {
    fun destination(context: Context): Uri? = context.getSharedPreferences("folder_receive", Context.MODE_PRIVATE)
        .getString("tree", null)?.let(Uri::parse)

    fun setDestination(context: Context, uri: Uri) {
        context.contentResolver.takePersistableUriPermission(uri,
            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        context.getSharedPreferences("folder_receive", Context.MODE_PRIVATE).edit().putString("tree", uri.toString()).apply()
    }

    private fun root(tree: Uri) = Docs.buildDocumentUriUsingTree(tree, Docs.getTreeDocumentId(tree))

    private data class Entry(val uri: Uri, val name: String, val directory: Boolean)

    private fun children(context: Context, tree: Uri, parent: Uri): List<Entry> {
        val query = Docs.buildChildDocumentsUriUsingTree(tree, Docs.getDocumentId(parent))
        val columns = arrayOf(Docs.Document.COLUMN_DOCUMENT_ID, Docs.Document.COLUMN_DISPLAY_NAME, Docs.Document.COLUMN_MIME_TYPE)
        return context.contentResolver.query(query, columns, null, null, null)?.use { c ->
            buildList {
                while (c.moveToNext()) add(Entry(Docs.buildDocumentUriUsingTree(tree, c.getString(0)), c.getString(1), c.getString(2) == Docs.Document.MIME_TYPE_DIR))
            }
        } ?: error("Cannot read folder")
    }

    /** Materialise the selected tree so offline delivery keeps the same structure. */
    fun copyTree(context: Context, tree: Uri, destination: File): File {
        val source = root(tree)
        val name = context.contentResolver.query(source, arrayOf(Docs.Document.COLUMN_DISPLAY_NAME), null, null, null)?.use {
            check(it.moveToFirst()) { "Folder is no longer available" }; it.getString(0)
        } ?: error("Cannot read folder name")
        fun copy(uri: Uri, target: File, directory: Boolean, depth: Int) {
            check(depth <= 128) { "Folder nesting is too deep" }
            if (directory) {
                check(target.mkdir()) { "Cannot create ${target.name}" }
                children(context, tree, uri).forEach { child ->
                    check(pathParts(child.name).size == 1) { "Invalid document name" }
                    copy(child.uri, File(target, child.name), child.directory, depth + 1)
                }
            } else {
                check(target.createNewFile()) { "Duplicate document name: ${target.name}" }
                context.contentResolver.openInputStream(uri)?.use { input -> target.outputStream().use { input.copyTo(it) } }
                    ?: error("Cannot read ${target.name}")
            }
        }
        check(pathParts(name).size == 1) { "Invalid folder name" }
        return File(destination, name).also { copy(source, it, true, 0) }
    }

    /** One receiver per session: keep renamed directory URIs for all descendants. */
    class Receiver(private val context: Context) {
        private val tree = destination(context) ?: error("Choose a receive folder on the home screen first")
        private val directories = mutableMapOf("" to root(tree))
        fun directory(path: String): Uri {
            val parts = pathParts(path)
            var key = ""
            var parent = directories.getValue("")
            for (part in parts) {
                key = if (key.isEmpty()) part else "$key/$part"
                parent = directories.getOrPut(key) { create(parent, part, Docs.Document.MIME_TYPE_DIR) }
            }
            return parent
        }
        fun file(path: String, mime: String): Uri {
            val parts = pathParts(path)
            val parent = if (parts.size == 1) directories.getValue("") else directory(parts.dropLast(1).joinToString("/"))
            return create(parent, parts.last(), mime)
        }
        private fun create(parent: Uri, name: String, mime: String): Uri {
            val existing = children(context, tree, parent).map { it.name }.toSet()
            var candidate = name
            var n = 1
            while (candidate in existing) { candidate = "$name (${n++})" }
            return Docs.createDocument(context.contentResolver, parent, mime, candidate) ?: error("Cannot create $candidate")
        }
    }
}
