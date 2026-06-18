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
import com.adrop.feature.fcm.FcmTokenStore
import com.adrop.data.trust.TrustRepository
import com.adrop.net.session.ProgressFn
import com.adrop.net.session.*
import com.adrop.net.tls.PinningTrustManager
import com.adrop.net.transport.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*

enum class ClipboardMimeMode(val mime: String, val label: String) {
    TEXT("text/plain", "Text"),
    IMAGE_PNG("image/png", "Image (PNG)"),
    HTML("text/html", "HTML"),
}

enum class SendPhase {
    PREPARING,
    TRANSFERRING,
}

private const val PREFS_NAME = "adrop_send_prefs"
private const val KEY_LAST_DEVICE_FP = "last_device_fingerprint"

data class SendUiState(
    val devices:        List<TrustedDevice> = emptyList(),
    val selectedDevice: TrustedDevice?      = null,
    val pickedUris:     List<Uri>           = emptyList(),
    val clipboardText:  String              = "",
    val clipboardMime:  ClipboardMimeMode   = ClipboardMimeMode.TEXT,
    val pickedImageUri: Uri?                = null,
    val isSending:      Boolean             = false,
    val sendPhase:      SendPhase?          = null,
    val result:         SendResult?         = null,
    /**
     * Human-readable status while files are being prepared or transferred.
     * PREPARING is set before manifest hashing, TRANSFERRING is populated by
     * the ProgressFn callback in Session.kt's sendFiles().
     * Null when no file send is active.
     *
     * TODO: wire Session.kt's fileProgress callback into state, then surface it
     * here as a 0..1 Float for determinate progress.
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

    private val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    private val _state = MutableStateFlow(SendUiState())
    val state: StateFlow<SendUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            trustRepo.devicesFlow.collect { devices ->
                val current = _state.value
                // Auto-select: keep current selection if still valid, otherwise
                // restore last-used device, otherwise pick the only device if there's one.
                val lastFp = prefs.getString(KEY_LAST_DEVICE_FP, null)
                val autoSelect = when {
                    current.selectedDevice != null && devices.any { it.fingerprint == current.selectedDevice.fingerprint } ->
                        current.selectedDevice
                    lastFp != null -> devices.find { it.fingerprint == lastFp }
                    devices.size == 1 -> devices.first()
                    else -> null
                }
                _state.update { it.copy(devices = devices, selectedDevice = autoSelect) }
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

    fun setClipboardMime(mode: ClipboardMimeMode) {
        _state.update { it.copy(clipboardMime = mode, pickedImageUri = null) }
    }

    fun setPickedImageUri(uri: Uri?) {
        _state.update { it.copy(pickedImageUri = uri) }
    }

    fun sendFiles() {
        val device = _state.value.selectedDevice ?: return
        val uris   = _state.value.pickedUris
        if (uris.isEmpty()) {
            _state.update { it.copy(result = SendResult.Error("No files selected")) }
            return
        }
        _state.update {
            it.copy(
                isSending        = true,
                sendPhase        = SendPhase.PREPARING,
                result           = null,
                transferProgress = "Preparing selected files…",
            )
        }
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching {
                    doSendFiles(
                        device      = device,
                        uris        = uris,
                        onPreparing = { msg ->
                            _state.update {
                                it.copy(sendPhase = SendPhase.PREPARING, transferProgress = msg)
                            }
                        },
                        progress    = { msg ->
                            // ProgressFn callback — runs on IO dispatcher, safe to update StateFlow.
                            _state.update {
                                it.copy(sendPhase = SendPhase.TRANSFERRING, transferProgress = msg)
                            }
                        },
                    )
                }
            }
            if (result.isSuccess) {
                prefs.edit().putString(KEY_LAST_DEVICE_FP, device.fingerprint).apply()
            }
            _state.update {
                it.copy(
                    isSending        = false,
                    sendPhase        = null,
                    transferProgress = null,
                    result = if (result.isSuccess) SendResult.Success
                             else SendResult.Error(result.exceptionOrNull()?.message ?: "Unknown error")
                )
            }
        }
    }

    fun sendClipboard() {
        val state  = _state.value
        val device = state.selectedDevice ?: return
        val mime   = state.clipboardMime

        val bytes: ByteArray
        when (mime) {
            ClipboardMimeMode.IMAGE_PNG -> {
                val uri = state.pickedImageUri
                if (uri == null) {
                    _state.update { it.copy(result = SendResult.Error("No image selected")) }
                    return
                }
                bytes = context.contentResolver.openInputStream(uri)?.use { it.readBytes() }
                    ?: run {
                        _state.update { it.copy(result = SendResult.Error("Cannot read image")) }
                        return
                    }
            }
            else -> {
                val text = state.clipboardText
                if (text.isBlank()) {
                    _state.update { it.copy(result = SendResult.Error("Clipboard is empty")) }
                    return
                }
                bytes = text.toByteArray(Charsets.UTF_8)
            }
        }

        _state.update { it.copy(isSending = true, sendPhase = null, transferProgress = null, result = null) }
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching { doSendClipboardBytes(device, bytes, mime.mime) }
            }
            if (result.isSuccess) {
                prefs.edit().putString(KEY_LAST_DEVICE_FP, device.fingerprint).apply()
            }
            _state.update {
                if (result.isSuccess) {
                    // Clear the composed input so the field doesn't keep stale
                    // text/image after a successful send.
                    it.copy(
                        isSending      = false,
                        clipboardText  = "",
                        pickedImageUri = null,
                        result         = SendResult.Success,
                    )
                } else {
                    it.copy(
                        isSending = false,
                        result    = SendResult.Error(result.exceptionOrNull()?.message ?: "Unknown error"),
                    )
                }
            }
        }
    }

    fun clearResult() {
        _state.update { it.copy(result = null) }
    }

    // ---------------------------------------------------------------------------
    // I/O workers
    // ---------------------------------------------------------------------------

    private suspend fun doSendFiles(
        device: TrustedDevice,
        uris: List<Uri>,
        onPreparing: ProgressFn? = null,
        progress: ProgressFn? = null,
    ) {
        val identity = IdentityStore.getOrCreate(context)
        val devices  = trustRepo.getAll()
        val trustMgr = PinningTrustManager(isTrusted = { fp -> devices.find { it.fingerprint == fp } })

        val resolver = context.contentResolver

        onPreparing?.invoke("Reading file details…")

        // Resolve names and sizes from content URIs.
        val names = uris.map { uri -> queryFileName(uri) }
        val sizes = uris.map { uri -> queryFileSize(uri) }

        // Build manifest by hashing files.
        onPreparing?.invoke("Hashing selected files…")
        val manifest = buildManifest(
            names    = names,
            sizes    = sizes,
            openFile = { i -> resolver.openInputStream(uris[i])!! },
        )

        onPreparing?.invoke("Connecting to ${device.name}…")
        val socket = dial(device.addr, identity, trustMgr)
        socket.use { s ->
            val out = s.outputStream.buffered()
            val inp = s.inputStream.buffered()

            // Hello exchange — include FCM token so PC can wake us next time.
            writeControl(out, Header(
                type        = MsgType.HELLO,
                version     = PROTOCOL_VERSION,
                fingerprint = identity.fingerprint,
                name        = android.os.Build.MODEL,
                addr        = "${localLanIp()}:${com.adrop.feature.receive.ReceiveForegroundService.LISTEN_PORT}",
                fcmToken    = FcmTokenStore.load(context),
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

    private suspend fun doSendClipboardBytes(device: TrustedDevice, bytes: ByteArray, mime: String) {
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
                fcmToken    = FcmTokenStore.load(context),
            ))
            readHeader(inp)

            sendClipboard(
                out  = out,
                inp  = inp,
                text = bytes,
                mime = mime,
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
