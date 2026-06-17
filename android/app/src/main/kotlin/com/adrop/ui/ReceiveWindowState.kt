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
    /** Current receive-window mode for UI copy and progress treatment. */
    val phase: ReceiveWindowPhase = ReceiveWindowPhase.CLOSED,
    /** Seconds left before the idle or resume-wait window closes. */
    val secondsRemaining: Int? = null,
    /** Number of currently active receive sessions. */
    val activeTransfers: Int = 0,
    /** Non-null when the receive window ended due to an error. */
    val lastError: String? = null,
)

enum class ReceiveWindowPhase {
    CLOSED,
    WAITING,
    RECEIVING,
    RESUME_WAIT,
}

object ReceiveWindowState {
    private val _flow = MutableStateFlow(ReceiveWindowUiState())
    val stateFlow: StateFlow<ReceiveWindowUiState> = _flow.asStateFlow()

    /** Called by ReceiveForegroundService when the window opens. */
    internal fun onStarted(secondsRemaining: Int) {
        _flow.value = ReceiveWindowUiState(
            isRunning = true,
            phase = ReceiveWindowPhase.WAITING,
            secondsRemaining = secondsRemaining,
            lastError = null,
        )
    }

    /** Called while the window is open and waiting for the first transfer. */
    internal fun onWaiting(secondsRemaining: Int) {
        _flow.value = ReceiveWindowUiState(
            isRunning = true,
            phase = ReceiveWindowPhase.WAITING,
            secondsRemaining = secondsRemaining,
        )
    }

    /** Called while one or more receive sessions are active. */
    internal fun onReceiving(activeTransfers: Int) {
        _flow.value = ReceiveWindowUiState(
            isRunning = true,
            phase = ReceiveWindowPhase.RECEIVING,
            activeTransfers = activeTransfers,
        )
    }

    /** Called after an interrupted transfer while waiting for a resume reconnect. */
    internal fun onResumeWait(secondsRemaining: Int) {
        _flow.value = ReceiveWindowUiState(
            isRunning = true,
            phase = ReceiveWindowPhase.RESUME_WAIT,
            secondsRemaining = secondsRemaining,
        )
    }

    /** Called by ReceiveForegroundService when the window closes normally. */
    internal fun onStopped() {
        _flow.value = ReceiveWindowUiState(lastError = _flow.value.lastError)
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
