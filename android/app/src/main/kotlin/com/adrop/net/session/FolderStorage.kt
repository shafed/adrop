package com.adrop.net.session

import android.content.Context
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
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

    /**
     * One receiver per session: folders land in Downloads via MediaStore, each
     * incoming root under a name adrop has not used yet. MediaStore cannot hold
     * an empty directory, so empty folders are not recreated.
     */
    class Receiver(private val context: Context) {
        private val roots = mutableMapOf<String, String>()
        private val used by lazy { usedRoots() }

        fun directory(path: String) {
            root(pathParts(path).first())
        }

        /** MediaStore RELATIVE_PATH for a file at [path], e.g. "Download/tree (1)/sub/". */
        fun relativePath(path: String): String {
            val parts = pathParts(path)
            val dirs = if (parts.size == 1) emptyList() else listOf(root(parts.first())) + parts.drop(1).dropLast(1)
            return (listOf(Environment.DIRECTORY_DOWNLOADS) + dirs).joinToString("/", postfix = "/")
        }

        private fun root(name: String) = roots.getOrPut(name) {
            var candidate = name
            var n = 1
            while (candidate in used) { candidate = "$name (${n++})" }
            used.add(candidate)
            candidate
        }

        private fun usedRoots(): MutableSet<String> {
            val prefix = Environment.DIRECTORY_DOWNLOADS + "/"
            return context.contentResolver.query(
                MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY),
                arrayOf(MediaStore.MediaColumns.RELATIVE_PATH),
                "${MediaStore.MediaColumns.RELATIVE_PATH} LIKE ?", arrayOf("$prefix%"), null,
            )?.use { c ->
                buildSet { while (c.moveToNext()) c.getString(0)?.removePrefix(prefix)?.substringBefore('/')?.let(::add) }
            }.orEmpty().toMutableSet()
        }
    }
}
