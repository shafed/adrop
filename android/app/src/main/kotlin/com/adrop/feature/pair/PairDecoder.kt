/**
 * Pairing QR decoder.
 *
 * The QR text is: adrop://pair?d=<base64url-no-padding>
 * where the base64url payload decodes to JSON:
 *   {"v":1,"n":<name>,"fp":<hex sha256>,"cert":<PEM string>,"addr":<host:port>}
 *
 * Anti-tamper: we parse the PEM cert, compute SHA-256 of its DER bytes, and
 * confirm it equals "fp". Reject on mismatch.
 *
 * This mirrors internal/pairing/pairing.go on the Go side.
 */
package com.adrop.feature.pair

import android.util.Base64
import com.adrop.data.trust.TrustedDevice
import org.json.JSONObject
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

// ---------------------------------------------------------------------------
// Data
// ---------------------------------------------------------------------------

private const val URI_SCHEME = "adrop://pair?d="

data class PairingPayload(
    val version:     Int,
    val name:        String,
    val fingerprint: String,  // hex sha256 of cert DER
    val certPem:     String,  // full PEM certificate
    val addr:        String,  // "host:port"
)

// ---------------------------------------------------------------------------
// Decoder
// ---------------------------------------------------------------------------

/**
 * Decodes and validates a pairing URI produced by the Go daemon.
 *
 * @throws PairingException on any parse or validation error.
 */
fun decodePairingUri(uri: String): PairingPayload {
    if (!uri.startsWith(URI_SCHEME)) {
        throw PairingException("not an adrop pairing URI")
    }
    val encoded = uri.removePrefix(URI_SCHEME)

    // base64url, no padding (matches Go's base64.RawURLEncoding)
    val jsonBytes = try {
        Base64.decode(encoded, Base64.URL_SAFE or Base64.NO_PADDING)
    } catch (e: Exception) {
        throw PairingException("base64url decode failed: ${e.message}", e)
    }

    val json = try {
        JSONObject(String(jsonBytes, Charsets.UTF_8))
    } catch (e: Exception) {
        throw PairingException("JSON parse failed: ${e.message}", e)
    }

    val version     = json.optInt("v", 0)
    val name        = json.optString("n")
    val fingerprint = json.optString("fp")
    val certPem     = json.optString("cert")
    val addr        = json.optString("addr")

    if (version < 1) throw PairingException("unsupported pairing version: $version")
    if (name.isBlank())        throw PairingException("missing device name in pairing payload")
    if (fingerprint.isBlank()) throw PairingException("missing fingerprint in pairing payload")
    if (certPem.isBlank())     throw PairingException("missing cert in pairing payload")
    if (addr.isBlank())        throw PairingException("missing addr in pairing payload")

    // Parse the PEM cert and verify the fingerprint anti-tamper.
    val certDer = try {
        val pemBody = certPem
            .lines()
            .filter { !it.startsWith("-----") }
            .joinToString("")
        android.util.Base64.decode(pemBody, android.util.Base64.DEFAULT)
    } catch (e: Exception) {
        throw PairingException("failed to decode PEM cert: ${e.message}", e)
    }

    // Also parse as X509 to make sure it's a valid cert.
    try {
        val cf = CertificateFactory.getInstance("X.509")
        cf.generateCertificate(certDer.inputStream()) as X509Certificate
    } catch (e: Exception) {
        throw PairingException("invalid X.509 certificate: ${e.message}", e)
    }

    val computedFp = MessageDigest.getInstance("SHA-256")
        .digest(certDer)
        .joinToString("") { "%02x".format(it) }

    if (!computedFp.equals(fingerprint, ignoreCase = true)) {
        throw PairingException(
            "fingerprint mismatch — pairing QR may be tampered: " +
                "cert=${computedFp.take(16)} claimed=${fingerprint.take(16)}"
        )
    }

    return PairingPayload(
        version     = version,
        name        = name,
        fingerprint = computedFp.lowercase(),
        certPem     = certPem,
        addr        = addr,
    )
}

/**
 * Converts a validated [PairingPayload] to a [TrustedDevice] ready for storage.
 */
fun PairingPayload.toTrustedDevice(): TrustedDevice = TrustedDevice(
    name        = name,
    fingerprint = fingerprint,
    addr        = addr,
)

class PairingException(message: String, cause: Throwable? = null) :
    Exception(message, cause)
