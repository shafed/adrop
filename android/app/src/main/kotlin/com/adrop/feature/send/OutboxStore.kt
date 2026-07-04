/**
 * Persistent outbox of sends that could not be delivered because the target
 * PC was offline / unreachable on the LAN at the time.
 *
 * Each item owns a directory under `filesDir/outbox/<id>/` holding a copy of
 * its payload (the picked files, or a single clipboard blob). We copy rather
 * than keep the original SAF content URIs because those permissions do not
 * survive process death / reboot, whereas the queue must.
 *
 * Delivery is driven by [SendWorker]; items are flushed when the target device
 * reappears on the network (mDNS) or on app start.
 */
package com.adrop.feature.send

import android.content.Context
import androidx.room.*
import kotlinx.coroutines.flow.Flow
import java.io.File

// ---------------------------------------------------------------------------
// Entity
// ---------------------------------------------------------------------------

enum class OutboxKind { FILES, CLIPBOARD }

@Entity(tableName = "outbox_items", indices = [Index(value = ["targetFingerprint"])])
data class OutboxItem(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val targetFingerprint: String,
    val kind:              OutboxKind,
    /** Directory holding this item's payload copies. */
    val payloadDir:        String,
    /** For CLIPBOARD: the mime type. Empty for FILES. */
    val mime:              String = "",
    val createdAt:         Long = System.currentTimeMillis(),
)

// ---------------------------------------------------------------------------
// DAO
// ---------------------------------------------------------------------------

@Dao
interface OutboxDao {
    @Query("SELECT * FROM outbox_items ORDER BY createdAt ASC")
    fun observeAll(): Flow<List<OutboxItem>>

    @Query("SELECT * FROM outbox_items WHERE targetFingerprint = :fp ORDER BY createdAt ASC")
    suspend fun forTarget(fp: String): List<OutboxItem>

    @Query("SELECT * FROM outbox_items ORDER BY createdAt ASC")
    suspend fun getAll(): List<OutboxItem>

    @Query("SELECT COUNT(*) FROM outbox_items")
    fun observeCount(): Flow<Int>

    @Insert
    suspend fun insert(item: OutboxItem): Long

    @Query("DELETE FROM outbox_items WHERE id = :id")
    suspend fun deleteById(id: Long)
}

// ---------------------------------------------------------------------------
// Database
// ---------------------------------------------------------------------------

@Database(entities = [OutboxItem::class], version = 1, exportSchema = false)
abstract class OutboxDatabase : RoomDatabase() {
    abstract fun dao(): OutboxDao

    companion object {
        @Volatile private var instance: OutboxDatabase? = null

        fun getInstance(context: Context): OutboxDatabase =
            instance ?: synchronized(this) {
                instance ?: Room.databaseBuilder(
                    context.applicationContext,
                    OutboxDatabase::class.java,
                    "adrop_outbox.db",
                ).build().also { instance = it }
            }
    }
}

// ---------------------------------------------------------------------------
// Store (application-level API)
// ---------------------------------------------------------------------------

class OutboxStore(private val context: Context, private val dao: OutboxDao) {

    val itemsFlow: Flow<List<OutboxItem>> = dao.observeAll()
    val countFlow: Flow<Int> = dao.observeCount()

    /**
     * Copies the bytes produced by [copyPayload] into a fresh payload dir and
     * records a queued [kind] send to [targetFingerprint].
     *
     * [copyPayload] receives the destination directory and must materialise the
     * payload there (one file per source, named with the user-facing filename).
     */
    suspend fun enqueue(
        targetFingerprint: String,
        kind: OutboxKind,
        mime: String = "",
        copyPayload: (dir: File) -> Unit,
    ): Long {
        val parent = File(context.filesDir, "outbox")
        parent.mkdirs()
        // Use a temp dir first, then record — so a crash mid-copy leaves no
        // dangling DB row pointing at a half-written payload.
        val dir = File(parent, "tmp-${System.nanoTime()}")
        dir.mkdirs()
        copyPayload(dir)
        val id = dao.insert(
            OutboxItem(
                targetFingerprint = targetFingerprint,
                kind              = kind,
                payloadDir        = dir.absolutePath,
                mime              = mime,
            )
        )
        return id
    }

    suspend fun forTarget(fp: String): List<OutboxItem> = dao.forTarget(fp)

    suspend fun getAll(): List<OutboxItem> = dao.getAll()

    /** Removes the DB row and deletes the payload directory. */
    suspend fun remove(item: OutboxItem) {
        dao.deleteById(item.id)
        File(item.payloadDir).deleteRecursively()
    }

    /** Returns the payload files of a FILES item, in queue order. */
    fun payloadFiles(item: OutboxItem): List<File> =
        File(item.payloadDir).listFiles()?.sortedBy { it.name } ?: emptyList()

    /**
     * The user-facing name for a payload file, stripping the "NNNN-" ordering
     * prefix added at enqueue time (see [SendViewModel.queueFiles]).
     */
    fun displayName(file: File): String =
        file.name.replaceFirst(Regex("^\\d{4}-"), "")

    /** Returns the single clipboard blob of a CLIPBOARD item. */
    fun clipboardBlob(item: OutboxItem): File =
        File(item.payloadDir, "clipboard.bin")

    companion object {
        @Volatile private var instance: OutboxStore? = null

        fun getInstance(context: Context): OutboxStore =
            instance ?: synchronized(this) {
                instance ?: OutboxStore(
                    context.applicationContext,
                    OutboxDatabase.getInstance(context).dao(),
                ).also { instance = it }
            }
    }
}
