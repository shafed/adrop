/**
 * Device identity: a self-signed TLS certificate + private key.
 *
 * Generated once on first run using the Android KeyStore system. The KeyStore
 * generates an EC P-256 (secp256r1) key pair and issues a self-signed cert
 * entirely inside the secure enclave — the private key never leaves it.
 *
 * We use P-256 rather than Ed25519 because Android's TLS stack (Conscrypt /
 * JSSE via SSLContext) does not support Ed25519 for TLS client/server auth
 * without a third-party provider. The wire protocol only pins SHA-256 of the
 * DER cert, so the algorithm does NOT need to match the Go daemon's Ed25519.
 *
 * Fingerprint = lowercase hex SHA-256 of cert.getEncoded() (DER bytes),
 * matching config.Fingerprint() on the Go side.
 */
package com.adrop.data.identity

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.X509Certificate
import java.security.PrivateKey
import java.security.spec.ECGenParameterSpec
import java.util.Date
import javax.security.auth.x500.X500Principal

/**
 * Lazily loaded device identity. Call [getOrCreate] to obtain the singleton.
 */
object IdentityStore {

    private const val KEYSTORE_ALIAS = "adrop_identity"
    private const val ANDROID_KEYSTORE = "AndroidKeyStore"

    @Volatile private var cached: DeviceIdentity? = null

    /**
     * Returns the device identity, creating it on first call.
     * Must be called on a background thread (I/O).
     */
    @Synchronized
    fun getOrCreate(context: Context): DeviceIdentity {
        cached?.let { return it }
        val id = loadOrGenerate()
        cached = id
        return id
    }

    private fun loadOrGenerate(): DeviceIdentity {
        val ks = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        return if (ks.containsAlias(KEYSTORE_ALIAS)) {
            loadFromKeyStore(ks)
        } else {
            generateAndStore(ks)
        }
    }

    private fun loadFromKeyStore(ks: KeyStore): DeviceIdentity {
        val cert       = ks.getCertificate(KEYSTORE_ALIAS) as X509Certificate
        val privateKey = ks.getKey(KEYSTORE_ALIAS, null) as PrivateKey
        return DeviceIdentity(cert, privateKey, sha256Hex(cert.encoded))
    }

    private fun generateAndStore(ks: KeyStore): DeviceIdentity {
        val kpg = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        kpg.initialize(
            KeyGenParameterSpec.Builder(
                KEYSTORE_ALIAS,
                KeyProperties.PURPOSE_SIGN or KeyProperties.PURPOSE_VERIFY,
            )
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(
                    KeyProperties.DIGEST_SHA256,
                    KeyProperties.DIGEST_SHA384,
                    KeyProperties.DIGEST_SHA512,
                    KeyProperties.DIGEST_NONE,
                )
                .setCertificateSubject(X500Principal("CN=adrop-android"))
                // 100-year validity: cert is pinned, not CA-validated.
                .setCertificateNotBefore(Date(System.currentTimeMillis() - 3_600_000L))
                .setCertificateNotAfter(Date(System.currentTimeMillis() + 100L * 365 * 24 * 3600 * 1000))
                .setCertificateSerialNumber(BigInteger.ONE)
                .setKeySize(256)
                .build()
        )
        kpg.generateKeyPair()

        // Re-load from the store to get the self-signed X509Certificate.
        val ks2    = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        val cert   = ks2.getCertificate(KEYSTORE_ALIAS) as X509Certificate
        val privKey = ks2.getKey(KEYSTORE_ALIAS, null) as PrivateKey
        return DeviceIdentity(cert, privKey, sha256Hex(cert.encoded))
    }

    /** SHA-256 of DER bytes, lowercase hex — matches Go's config.Fingerprint(). */
    fun sha256Hex(derBytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256")
            .digest(derBytes)
            .joinToString("") { "%02x".format(it) }
}

/**
 * Immutable snapshot of this device's identity.
 */
data class DeviceIdentity(
    val certificate: X509Certificate,
    val privateKey:  PrivateKey,
    /** lowercase hex SHA-256 of cert DER — the value pinned by peers. */
    val fingerprint: String,
)
