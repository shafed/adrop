/**
 * In-process singleton that publishes the current receive-window state
 * from [ReceiveForegroundService] to any composable that wants to display it.
 *
 * Using a plain object / StateFlow keeps this independent of the service
 * lifecycle and avoids needing a Binder or BroadcastReceiver just to pass
 * countdown seconds to the Home screen.
 *
 * The service writes to this object; composables read it via [stateFlow].
 */
package com.adrop.ui

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

data class ReceiveWindowUiState(
    /** True while the foreground service is running. */
    val isRunning: Boolean = false,
    /** Non-null when the receive window ended due to an error. */
    val lastError: String? = null,
)

object ReceiveWindowState {
    private val _flow = MutableStateFlow(ReceiveWindowUiState())
    val stateFlow: StateFlow<ReceiveWindowUiState> = _flow.asStateFlow()

    /** Called by ReceiveForegroundService when the window opens. */
    internal fun onStarted() {
        _flow.value = ReceiveWindowUiState(isRunning = true, lastError = null)
    }

    /** Called by ReceiveForegroundService when the window closes normally. */
    internal fun onStopped() {
        _flow.value = ReceiveWindowUiState(isRunning = false)
    }

    /** Called by ReceiveForegroundService when the window closes due to an error. */
    internal fun onError(message: String) {
        _flow.value = ReceiveWindowUiState(isRunning = false, lastError = message)
    }

    /** Called by the UI once it has consumed the error so it isn't shown again. */
    fun clearError() {
        _flow.value = _flow.value.copy(lastError = null)
    }
}
