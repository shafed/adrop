/**
 * ViewModel for the Send screen.
 *
 * Handles:
 *   - Selecting files via SAF (ACTION_OPEN_DOCUMENT_MULTIPLE)
 *   - Choosing a paired device
 *   - Sending files or clipboard text to the selected device
 */
package com.adrop.feature.send

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.adrop.data.identity.IdentityStore
import com.adrop.data.proto.*
import com.adrop.data.trust.TrustedDevice
import com.adrop.data.trust.TrustRepository
import com.adrop.net.session.ProgressFn
import com.adrop.net.session.*
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*

data class SendUiState(
    val devices:        List<TrustedDevice> = emptyList(),
    val selectedDevice: TrustedDevice?      = null,
    val pickedUris:     List<Uri>           = emptyList(),
    val clipboardText:  String              = "",
    val isSending:      Boolean             = false,
    val result:         SendResult?         = null,
    /**
     * Human-readable progress message while a file transfer is in flight.
     * Populated by the ProgressFn callback in Session.kt's sendFiles().
     * Null when no transfer is active.
     *
     * TODO: replace/augment with per-file byte-level progress once Session.kt
     * exposes a bytesTransferred callback in addition to the message callback.
     * See net/session/Session.kt sendFiles() — add a (fileIndex, bytesSent, totalBytes)
     * overload to ProgressFn there, then surface it here as a 0..1 Float.
     */
    val transferProgress: String?           = null,
)

sealed class SendResult {
    object Success : SendResult()
    data class Error(val message: String) : SendResult()
}

class SendViewModel(
    private val context: Context,
    private val trustRepo: TrustRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SendUiState())
    val state: StateFlow<SendUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            trustRepo.devicesFlow.collect { devices ->
                _state.update { it.copy(devices = devices) }
            }
        }
    }

    fun selectDevice(device: TrustedDevice) {
        _state.update { it.copy(selectedDevice = device, result = null) }
    }

    fun setPickedUris(uris: List<Uri>) {
        _state.update { it.copy(pickedUris = uris, result = null) }
    }

    fun setClipboardText(text: String) {
        _state.update { it.copy(clipboardText = text) }
    }

    fun sendFiles() {
        val device = _state.value.selectedDevice ?: return
        val uris   = _state.value.pickedUris
        if (uris.isEmpty()) {
            _state.update { it.copy(result = SendResult.Error("No files selected")) }
            return
        }
        _state.update { it.copy(isSending = true, result = null, transferProgress = null) }
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching {
                    doSendFiles(device, uris) { msg ->
                        // ProgressFn callback — runs on IO dispatcher, safe to update StateFlow.
                        _state.update { it.copy(transferProgress = msg) }
                    }
                }
            }
            _state.update {
                it.copy(
                    isSending        = false,
                    transferProgress = null,
                    result = if (result.isSuccess) SendResult.Success
                             else SendResult.Error(result.exceptionOrNull()?.message ?: "Unknown error")
                )
            }
        }
    }

    fun sendClipboard() {
        val device = _state.value.selectedDevice ?: return
        val text   = _state.value.clipboardText
        if (text.isBlank()) {
            _state.update { it.copy(result = SendResult.Error("Clipboard is empty")) }
            return
        }
        _state.update { it.copy(isSending = true, result = null) }
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching { doSendClipboard(device, text) }
            }
            _state.update {
                it.copy(
                    isSending = false,
                    result = if (result.isSuccess) SendResult.Success
                             else SendResult.Error(result.exceptionOrNull()?.message ?: "Unknown error")
                )
            }
        }
    }

    fun clearResult() {
        _state.update { it.copy(result = null) }
    }

    // ---------------------------------------------------------------------------
    // I/O workers
    // ---------------------------------------------------------------------------

    private suspend fun doSendFiles(device: TrustedDevice, uris: List<Uri>, progress: ProgressFn? = null) {
        val identity = IdentityStore.getOrCreate(context)
        val devices  = trustRepo.getAll()
        val trustMgr = PinningTrustManager(isTrusted = { fp -> devices.find { it.fingerprint == fp } })

        val resolver = context.contentResolver

        // Resolve names and sizes from content URIs.
        val names = uris.map { uri -> queryFileName(uri) }
        val sizes = uris.map { uri -> queryFileSize(uri) }

        // Build manifest by hashing files.
        val manifest = buildManifest(
            names    = names,
            sizes    = sizes,
            openFile = { i -> resolver.openInputStream(uris[i])!! },
        )

        val socket = dial(device.addr, identity, trustMgr)
        socket.use { s ->
            val out = s.outputStream.buffered()
            val inp = s.inputStream.buffered()

            // Hello exchange.
            writeControl(out, Header(
                type        = MsgType.HELLO,
                version     = PROTOCOL_VERSION,
                fingerprint = identity.fingerprint,
                name        = android.os.Build.MODEL,
                addr        = "${localLanIp()}:${com.adrop.feature.receive.ReceiveForegroundService.LISTEN_PORT}",
            ))
            readHeader(inp)  // their hello

            sendFiles(
                out      = out,
                inp      = inp,
                manifest = manifest,
                openFile = { i -> resolver.openInputStream(uris[i])!! },
                progress = progress,
            )
        }
    }

    private suspend fun doSendClipboard(device: TrustedDevice, text: String) {
        val identity = IdentityStore.getOrCreate(context)
        val devices  = trustRepo.getAll()
        val trustMgr = PinningTrustManager(isTrusted = { fp -> devices.find { it.fingerprint == fp } })

        val socket = dial(device.addr, identity, trustMgr)
        socket.use { s ->
            val out = s.outputStream.buffered()
            val inp = s.inputStream.buffered()

            writeControl(out, Header(
                type        = MsgType.HELLO,
                version     = PROTOCOL_VERSION,
                fingerprint = identity.fingerprint,
                name        = android.os.Build.MODEL,
                addr        = "${localLanIp()}:${com.adrop.feature.receive.ReceiveForegroundService.LISTEN_PORT}",
            ))
            readHeader(inp)

            sendClipboard(
                out  = out,
                inp  = inp,
                text = text.toByteArray(Charsets.UTF_8),
            )
        }
    }

    // ---------------------------------------------------------------------------
    // Content URI helpers
    // ---------------------------------------------------------------------------

    private fun queryFileName(uri: Uri): String {
        val cursor = context.contentResolver.query(uri, null, null, null, null)
        return cursor?.use { c ->
            if (c.moveToFirst()) {
                val idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (idx >= 0) c.getString(idx) else "file"
            } else "file"
        } ?: uri.lastPathSegment ?: "file"
    }

    private fun queryFileSize(uri: Uri): Long {
        val cursor = context.contentResolver.query(uri, null, null, null, null)
        return cursor?.use { c ->
            if (c.moveToFirst()) {
                val idx = c.getColumnIndex(OpenableColumns.SIZE)
                if (idx >= 0 && !c.isNull(idx)) c.getLong(idx) else 0L
            } else 0L
        } ?: 0L
    }

    companion object {
        fun factory(context: Context) = object : ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : ViewModel> create(modelClass: Class<T>): T =
                SendViewModel(context.applicationContext, TrustRepository.getInstance(context)) as T
        }
    }
}
