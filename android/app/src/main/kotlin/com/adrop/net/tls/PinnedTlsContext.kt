/**
 * Mutual-TLS with certificate pinning.
 *
 * Trust model (mirrors the Go daemon's transport package):
 *   - Neither side trusts a CA. Each side presents its own self-signed cert.
 *   - A peer is accepted ONLY if SHA-256(peerLeafCert.DER) is in the pinned set.
 *   - During an open pairing window (phone scanning the PC's QR), we need to
 *     accept the PC's cert before it is stored. [allowedPairingFingerprint] handles
 *     this: if set, that single fingerprint is also accepted.
 *
 * Produced SSLContext can be used with both SSLServerSocket and SSLSocket.
 */
package com.adrop.net.tls

import com.adrop.data.identity.DeviceIdentity
import com.adrop.data.identity.IdentityStore
import com.adrop.data.trust.TrustedDevice
import java.net.Socket
import java.security.KeyStore
import java.security.MessageDigest
import java.security.Principal
import java.security.PrivateKey
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.*

// ---------------------------------------------------------------------------
// Fingerprint utility
// ---------------------------------------------------------------------------

fun sha256Hex(derBytes: ByteArray): String {
    val md = MessageDigest.getInstance("SHA-256")
    return md.digest(derBytes).joinToString("") { "%02x".format(it) }
}

// ---------------------------------------------------------------------------
// KeyManager — presents our device cert
// ---------------------------------------------------------------------------

private class DeviceKeyManager(private val identity: DeviceIdentity) : X509KeyManager {

    private val alias = "adrop"

    override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?) = arrayOf(alias)
    override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?) = arrayOf(alias)
    override fun chooseClientAlias(keyTypes: Array<out String>?, issuers: Array<out Principal>?, socket: Socket?) = alias
    override fun chooseServerAlias(keyType: String?, issuers: Array<out Principal>?, socket: Socket?) = alias
    override fun getCertificateChain(alias: String?) = arrayOf(identity.certificate)
    override fun getPrivateKey(alias: String?): PrivateKey = identity.privateKey
}

// ---------------------------------------------------------------------------
// TrustManager — pins peer cert fingerprints
// ---------------------------------------------------------------------------

/**
 * @param isTrusted  synchronous lookup; returns non-null if the fingerprint is pinned.
 * @param pairingFp  if non-null, this fingerprint is also accepted (open pairing window).
 */
class PinningTrustManager(
    private val isTrusted: (String) -> TrustedDevice?,
    @Volatile var pairingFp: String? = null,
) : X509TrustManager {

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()

    override fun checkClientTrusted(chain: Array<out X509Certificate>, authType: String) =
        verifyPin(chain)

    override fun checkServerTrusted(chain: Array<out X509Certificate>, authType: String) =
        verifyPin(chain)

    private fun verifyPin(chain: Array<out X509Certificate>) {
        if (chain.isEmpty()) throw CertificateException("empty certificate chain")
        val leaf = chain[0]
        val fp = sha256Hex(leaf.encoded)

        if (isTrusted(fp) != null) return   // in trusted set
        if (fp == pairingFp) return          // open pairing window accepts this one

        throw CertificateException("peer certificate not pinned (fingerprint=${fp.take(16)}…)")
    }
}

// ---------------------------------------------------------------------------
// SSLContext factory
// ---------------------------------------------------------------------------

/**
 * Builds an [SSLContext] configured for mutual TLS with key pinning.
 *
 * @param identity          this device's cert + key
 * @param trustManager      [PinningTrustManager] with the current trusted set
 */
fun buildSslContext(
    identity: DeviceIdentity,
    trustManager: PinningTrustManager,
): SSLContext {
    val ctx = SSLContext.getInstance("TLSv1.3")
    ctx.init(
        arrayOf(DeviceKeyManager(identity)),
        arrayOf(trustManager),
        null,
    )
    return ctx
}

/**
 * Configures an [SSLParameters] to require TLS 1.3, no hostname check, and
 * mutual authentication.
 */
fun serverSslParameters(): SSLParameters = SSLParameters().apply {
    protocols         = arrayOf("TLSv1.3")
    needClientAuth    = true  // mutual TLS
    // null disables endpoint (hostname) identification; we pin certificates
    // ourselves via PinningTrustManager, so the usual hostname check is moot.
    endpointIdentificationAlgorithm = null
}

fun clientSslParameters(): SSLParameters = SSLParameters().apply {
    protocols         = arrayOf("TLSv1.3")
    endpointIdentificationAlgorithm = null  // no hostname verification (we pin certs)
}
