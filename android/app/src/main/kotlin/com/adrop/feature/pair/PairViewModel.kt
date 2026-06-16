package com.adrop.feature.pair

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.adrop.data.identity.IdentityStore
import com.adrop.data.proto.Header
import com.adrop.data.proto.MsgType
import com.adrop.data.proto.PROTOCOL_VERSION
import com.adrop.data.proto.writeControl
import com.adrop.data.trust.TrustRepository
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.dial
import com.adrop.net.transport.localLanIp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed class PairState {
    object Idle : PairState()
    object Scanning : PairState()
    data class Decoded(val payload: PairingPayload) : PairState()
    object Connecting : PairState()
    data class Success(val deviceName: String) : PairState()
    data class Error(val message: String) : PairState()
}

class PairViewModel(
    private val context: Context,
    private val trustRepo: TrustRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<PairState>(PairState.Idle)
    val state: StateFlow<PairState> = _state.asStateFlow()

    /** Called by the camera screen when a QR barcode is scanned. */
    fun onQrScanned(rawValue: String) {
        if (_state.value !is PairState.Scanning && _state.value !is PairState.Idle) return

        val payload = try {
            decodePairingUri(rawValue)
        } catch (e: PairingException) {
            _state.value = PairState.Error("Invalid QR: ${e.message}")
            return
        }

        _state.value = PairState.Decoded(payload)
        confirmPairing(payload)
    }

    fun startScanning() {
        _state.value = PairState.Scanning
    }

    fun reset() {
        _state.value = PairState.Idle
    }

    private fun confirmPairing(payload: PairingPayload) {
        _state.value = PairState.Connecting
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching { doPairing(payload) }
            }
            result.fold(
                onSuccess = { _state.value = PairState.Success(payload.name) },
                onFailure = { _state.value = PairState.Error(it.message ?: "Pairing failed") },
            )
        }
    }

    private suspend fun doPairing(payload: PairingPayload) {
        val identity = IdentityStore.getOrCreate(context)

        // Store the PC as trusted FIRST (so the TLS pinning accepts its cert).
        trustRepo.add(payload.toTrustedDevice())

        // Build a TrustManager that trusts the newly-added device.
        val devices = trustRepo.getAll()
        val trustManager = PinningTrustManager(
            isTrusted = { fp -> devices.find { it.fingerprint == fp } }
        )

        // Back-connect to the PC so it can learn and pin our fingerprint.
        val socket = dial(payload.addr, identity, trustManager)
        try {
            val out = socket.outputStream.buffered()
            val inp = socket.inputStream.buffered()

            // Send Hello so the PC's pairing window pins our cert.
            writeControl(out, Header(
                type        = MsgType.HELLO,
                version     = PROTOCOL_VERSION,
                fingerprint = identity.fingerprint,
                name        = android.os.Build.MODEL,
                addr        = "${localLanIp()}:${DEFAULT_LISTEN_PORT}",
            ))

            // Read their Hello in return.
            com.adrop.data.proto.readHeader(inp)
        } finally {
            socket.close()
        }
    }

    companion object {
        const val DEFAULT_LISTEN_PORT = 7777

        fun factory(context: Context) = object : ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : ViewModel> create(modelClass: Class<T>): T =
                PairViewModel(
                    context.applicationContext,
                    TrustRepository.getInstance(context),
                ) as T
        }
    }
}
