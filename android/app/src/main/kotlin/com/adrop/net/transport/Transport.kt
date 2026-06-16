/**
 * Low-level connect (dial) and listen operations over pinned TLS.
 *
 * Mirrors the Go daemon's internal/transport/transport.go.
 */
package com.adrop.net.transport

import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.tls.buildSslContext
import com.adrop.net.tls.clientSslParameters
import com.adrop.net.tls.serverSslParameters
import com.adrop.net.tls.sha256Hex
import com.adrop.data.identity.DeviceIdentity
import java.net.InetSocketAddress
import javax.net.ssl.SSLServerSocket
import javax.net.ssl.SSLSocket

/**
 * Dials [addr] (format "host:port") over TLS and returns the open [SSLSocket].
 *
 * The caller is responsible for closing the socket.
 */
fun dial(
    addr: String,
    identity: DeviceIdentity,
    trustManager: PinningTrustManager,
    connectTimeoutMs: Int = 10_000,
): SSLSocket {
    val (host, port) = parseHostPort(addr)
    val ctx = buildSslContext(identity, trustManager)
    val factory = ctx.socketFactory

    val socket = factory.createSocket() as SSLSocket
    socket.sslParameters = clientSslParameters()
    socket.connect(InetSocketAddress(host, port), connectTimeoutMs)
    socket.startHandshake()  // explicit handshake so errors surface here
    return socket
}

/**
 * Creates a TLS server socket listening on [listenAddr].
 * Requires client certificates (mutual TLS).
 */
fun listen(
    listenAddr: String,
    identity: DeviceIdentity,
    trustManager: PinningTrustManager,
): SSLServerSocket {
    val (host, port) = parseHostPort(listenAddr)
    val ctx = buildSslContext(identity, trustManager)
    val factory = ctx.serverSocketFactory

    val serverSocket = factory.createServerSocket() as SSLServerSocket
    serverSocket.sslParameters = serverSslParameters()
    serverSocket.reuseAddress = true
    serverSocket.bind(InetSocketAddress(host, port))
    return serverSocket
}

/**
 * Extracts the peer fingerprint from a just-handshaked socket.
 */
fun peerFingerprint(socket: SSLSocket): String {
    val session = socket.session
    val certs = session.peerCertificates
    if (certs.isEmpty()) error("no peer certificate after handshake")
    return sha256Hex(certs[0].encoded)
}

private fun parseHostPort(addr: String): Pair<String, Int> {
    val lastColon = addr.lastIndexOf(':')
    require(lastColon > 0) { "invalid addr (expected host:port): $addr" }
    val host = addr.substring(0, lastColon)
    val port = addr.substring(lastColon + 1).toInt()
    return host to port
}

/**
 * Returns the local TCP address string "host:port" that [socket] is bound to.
 * Useful for advertising our listen address in Hello messages.
 */
fun localListenAddr(socket: SSLServerSocket): String {
    val ia = socket.inetAddress
    val host = if (ia.isAnyLocalAddress) localLanIp() else ia.hostAddress
    return "$host:${socket.localPort}"
}

/** Best-effort: pick a non-loopback IPv4 address on the active LAN interface. */
fun localLanIp(): String {
    return try {
        java.net.NetworkInterface.getNetworkInterfaces()
            ?.asSequence()
            ?.flatMap { ni -> ni.inetAddresses.asSequence() }
            ?.firstOrNull { a ->
                !a.isLoopbackAddress &&
                    !a.isLinkLocalAddress &&
                    a is java.net.Inet4Address
            }
            ?.hostAddress
            ?: "0.0.0.0"
    } catch (_: Exception) {
        "0.0.0.0"
    }
}
