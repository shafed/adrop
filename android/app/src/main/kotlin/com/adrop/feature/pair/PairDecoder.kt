/**
 * Pairing QR decoder.
 *
 * The QR text is: adrop://pair?d=<base64url-no-padding>. Current payloads are
 * compact binary version 2:
 *   [version=2][nameLen][name][addrLen][addr][32-byte fp][2-byte certLen][cert DER]
 *
 * Legacy version-1 payloads are still accepted. They decode to JSON:
 *   {"v":1,"n":<name>,"fp":<hex sha256>,"cert":<PEM string>,"addr":<host:port>}
 *
 * Anti-tamper: we parse the cert, compute SHA-256 of its DER bytes, and
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
    val payloadBytes = try {
        Base64.decode(encoded, Base64.URL_SAFE or Base64.NO_PADDING)
    } catch (e: Exception) {
        throw PairingException("base64url decode failed: ${e.message}", e)
    }

    return if (payloadBytes.firstOrNull()?.toInt() == '{'.code) {
        decodeJsonPayload(payloadBytes)
    } else {
        decodeCompactPayload(payloadBytes)
    }
}

private fun decodeJsonPayload(jsonBytes: ByteArray): PairingPayload {
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

    return validatePayload(version, name, fingerprint, certDer, certPem, addr)
}

private fun decodeCompactPayload(payloadBytes: ByteArray): PairingPayload {
    var index = 0
    fun takeByte(label: String): Int {
        if (index >= payloadBytes.size) throw PairingException("compact payload missing $label")
        return payloadBytes[index++].toInt() and 0xFF
    }
    fun takeBytes(count: Int, label: String): ByteArray {
        if (count <= 0 || index + count > payloadBytes.size) {
            throw PairingException("compact payload bad $label")
        }
        return payloadBytes.copyOfRange(index, index + count).also {
            index += count
        }
    }
    fun takeString(label: String): String {
        val len = takeByte("$label length")
        return takeBytes(len, label).toString(Charsets.UTF_8)
    }

    val version = takeByte("version")
    if (version != 2) throw PairingException("unsupported compact pairing version: $version")

    val name = takeString("device name")
    val addr = takeString("addr")
    val fingerprint = takeBytes(32, "fingerprint")
        .joinToString("") { "%02x".format(it.toInt() and 0xFF) }
    if (index + 2 > payloadBytes.size) throw PairingException("compact payload missing cert length")
    val certLen = ((payloadBytes[index].toInt() and 0xFF) shl 8) or
        (payloadBytes[index + 1].toInt() and 0xFF)
    index += 2
    val certDer = takeBytes(certLen, "cert")
    if (index != payloadBytes.size) throw PairingException("compact payload has trailing bytes")

    return validatePayload(version, name, fingerprint, certDer, certPemFromDer(certDer), addr)
}

private fun validatePayload(
    version: Int,
    name: String,
    fingerprint: String,
    certDer: ByteArray,
    certPem: String,
    addr: String,
): PairingPayload {
    if (name.isBlank())        throw PairingException("missing device name in pairing payload")
    if (fingerprint.isBlank()) throw PairingException("missing fingerprint in pairing payload")
    if (certDer.isEmpty())     throw PairingException("missing cert in pairing payload")
    if (addr.isBlank())        throw PairingException("missing addr in pairing payload")

    // Also parse as X509 to make sure it's a valid cert.
    try {
        val cf = CertificateFactory.getInstance("X.509")
        cf.generateCertificate(certDer.inputStream()) as X509Certificate
    } catch (e: Exception) {
        throw PairingException("invalid X.509 certificate: ${e.message}", e)
    }

    val computedFp = MessageDigest.getInstance("SHA-256")
        .digest(certDer)
        .joinToString("") { "%02x".format(it.toInt() and 0xFF) }

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

private fun certPemFromDer(certDer: ByteArray): String {
    val body = Base64.encodeToString(certDer, Base64.NO_WRAP)
        .chunked(64)
        .joinToString("\n")
    return "-----BEGIN CERTIFICATE-----\n$body\n-----END CERTIFICATE-----\n"
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
