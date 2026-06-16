/**
 * Persistent store of paired ("trusted") devices.
 *
 * Backed by Room so queries are reactive (Flow). Each device entry holds:
 *   name        — human label shown in the UI
 *   fingerprint — SHA-256 of the peer's DER cert (the actual pin)
 *   addr        — last-known "host:port" used to dial
 *   pairedAt    — epoch millis when the pairing was completed
 *
 * Thread-safety: Room handles this internally via coroutines / DAOs.
 */
package com.adrop.data.trust

import android.content.Context
import androidx.room.*
import kotlinx.coroutines.flow.Flow

// ---------------------------------------------------------------------------
// Entity
// ---------------------------------------------------------------------------

@Entity(tableName = "trusted_devices", indices = [Index(value = ["fingerprint"], unique = true)])
data class TrustedDevice(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val name:        String,
    val fingerprint: String,  // lowercase hex SHA-256 of peer's DER cert
    val addr:        String,  // "host:port" dialable address
    val pairedAt:    Long = System.currentTimeMillis(),
)

// ---------------------------------------------------------------------------
// DAO
// ---------------------------------------------------------------------------

@Dao
interface TrustedDeviceDao {

    @Query("SELECT * FROM trusted_devices ORDER BY pairedAt DESC")
    fun observeAll(): Flow<List<TrustedDevice>>

    @Query("SELECT * FROM trusted_devices ORDER BY pairedAt DESC")
    suspend fun getAll(): List<TrustedDevice>

    @Query("SELECT * FROM trusted_devices WHERE fingerprint = :fp LIMIT 1")
    suspend fun findByFingerprint(fp: String): TrustedDevice?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(device: TrustedDevice)

    @Query("DELETE FROM trusted_devices WHERE fingerprint = :fp")
    suspend fun deleteByFingerprint(fp: String)

    @Query("DELETE FROM trusted_devices WHERE id = :id")
    suspend fun deleteById(id: Long)

    @Query("UPDATE trusted_devices SET addr = :addr WHERE fingerprint = :fp")
    suspend fun updateAddr(fp: String, addr: String)
}

// ---------------------------------------------------------------------------
// Database
// ---------------------------------------------------------------------------

@Database(entities = [TrustedDevice::class], version = 1, exportSchema = false)
abstract class TrustDatabase : RoomDatabase() {
    abstract fun dao(): TrustedDeviceDao

    companion object {
        @Volatile private var instance: TrustDatabase? = null

        fun getInstance(context: Context): TrustDatabase =
            instance ?: synchronized(this) {
                instance ?: Room.databaseBuilder(
                    context.applicationContext,
                    TrustDatabase::class.java,
                    "adrop_trust.db",
                ).build().also { instance = it }
            }
    }
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

/**
 * Application-level API over the trust database.
 *
 * Also provides in-memory fast-path for TLS handshake verification via
 * [isTrusted], which must be callable from any thread without suspending.
 */
class TrustRepository(private val dao: TrustedDeviceDao) {

    // volatile snapshot kept fresh for synchronous handshake checks
    @Volatile private var fingerprintCache: Map<String, TrustedDevice> = emptyMap()

    /** Reactive list of all paired devices. */
    val devicesFlow: Flow<List<TrustedDevice>> = dao.observeAll()

    /** Synchronous check — safe to call from SSLContext's VerifyPeerCertificate. */
    fun isTrusted(fingerprint: String): TrustedDevice? = fingerprintCache[fingerprint]

    /**
     * Refresh the in-memory cache from what was just emitted by [devicesFlow].
     * Called by the ViewModel whenever the flow emits.
     */
    fun updateCache(devices: List<TrustedDevice>) {
        fingerprintCache = devices.associateBy { it.fingerprint }
    }

    suspend fun add(device: TrustedDevice) {
        dao.upsert(device)
    }

    suspend fun remove(id: Long) {
        dao.deleteById(id)
    }

    suspend fun updateAddr(fingerprint: String, addr: String) {
        dao.updateAddr(fingerprint, addr)
    }

    suspend fun findByFingerprint(fp: String): TrustedDevice? =
        dao.findByFingerprint(fp)

    suspend fun getAll(): List<TrustedDevice> = dao.getAll()

    companion object {
        @Volatile private var instance: TrustRepository? = null

        fun getInstance(context: Context): TrustRepository =
            instance ?: synchronized(this) {
                instance ?: TrustRepository(
                    TrustDatabase.getInstance(context).dao()
                ).also { instance = it }
            }
    }
}
