/**
 * Home screen — the app's main entry point.
 *
 * Shows:
 *   - "Open to receive" toggle (starts/stops the foreground service)
 *     with a live countdown of the remaining receive-window time
 *   - Quick-access buttons: Send, Devices, Pair
 *   - Snackbar for receive-window errors surfaced by ReceiveForegroundService
 */
package com.adrop.ui.screens

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.navigation.NavBackStackEntry
import com.adrop.feature.receive.ReceiveForegroundService
import com.adrop.ui.ReceiveWindowPhase
import com.adrop.ui.ReceiveWindowState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    onNavigateSend:    () -> Unit,
    onNavigateDevices: () -> Unit,
    onNavigatePair:    () -> Unit,
    navBackStackEntry: NavBackStackEntry? = null,
) {
    val context = LocalContext.current

    // Observe live service state (isRunning + countdown) from ReceiveWindowState.
    val receiveState by ReceiveWindowState.stateFlow.collectAsState()
    val receiveActive = receiveState.isRunning

    // Snackbar for receive-window errors and pair-success confirmations.
    val snackbarHostState = remember { SnackbarHostState() }
    var notificationPermissionRequestInFlight by remember { mutableStateOf(false) }
    var notificationPermissionDeniedEvents by remember { mutableStateOf(0) }

    val notificationPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        notificationPermissionRequestInFlight = false
        if (granted) {
            notificationPermissionDeniedEvents = 0
            startReceiveWindow(context)
        } else {
            notificationPermissionDeniedEvents += 1
        }
    }

    val notificationPermissionDenied =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            notificationPermissionDeniedEvents > 0 &&
            !hasNotificationPermission(context)

    val countdownText = formatSeconds(receiveState.secondsRemaining)
    val receiveTitle = when {
        notificationPermissionRequestInFlight -> "Allow notifications"
        notificationPermissionDenied && !receiveActive -> "Notifications off"
        else -> when (receiveState.phase) {
            ReceiveWindowPhase.WAITING -> "Waiting for transfer"
            ReceiveWindowPhase.RECEIVING -> {
                if (receiveState.activeTransfers > 1) {
                    "Receiving ${receiveState.activeTransfers} transfers…"
                } else if (receiveState.activeTransfers == 1) {
                    "Receiving transfer…"
                } else {
                    "Finishing transfer…"
                }
            }
            ReceiveWindowPhase.RESUME_WAIT -> "Transfer interrupted"
            ReceiveWindowPhase.CLOSED -> "Open to receive"
        }
    }
    val receiveSubtitle = when {
        notificationPermissionRequestInFlight ->
            "Waiting for Android notification permission"
        notificationPermissionDenied && !receiveActive ->
            "Enable notifications before opening a receive window"
        else -> when (receiveState.phase) {
            ReceiveWindowPhase.WAITING ->
                "Closes in $countdownText if no transfer starts"
            ReceiveWindowPhase.RECEIVING ->
                "Window stays open until the active transfer finishes"
            ReceiveWindowPhase.RESUME_WAIT ->
                "Waiting $countdownText for the sender to reconnect"
            ReceiveWindowPhase.CLOSED ->
                "Opens for 60s while waiting"
        }
    }
    val countdownProgress = (
        (receiveState.secondsRemaining ?: 0).toFloat() / ReceiveForegroundService.GRACE_SEC
    ).coerceIn(0f, 1f)

    // Receive-window errors from ReceiveForegroundService.
    LaunchedEffect(receiveState.lastError) {
        receiveState.lastError?.let { error ->
            snackbarHostState.showSnackbar("Receive error: $error")
            ReceiveWindowState.clearError()
        }
    }

    LaunchedEffect(notificationPermissionDeniedEvents) {
        if (notificationPermissionDeniedEvents > 0) {
            val result = snackbarHostState.showSnackbar(
                message = "Notifications are off, so the receive window was not opened.",
                actionLabel = "Settings",
                duration = SnackbarDuration.Long,
            )
            if (result == SnackbarResult.ActionPerformed) {
                openNotificationSettings(context)
            }
        }
    }

    // "Paired!" confirmation from ScanScreen via savedStateHandle.
    val pairedDevice by navBackStackEntry
        ?.savedStateHandle
        ?.getStateFlow<String?>("pairedDevice", null)
        ?.collectAsState()
        ?: remember { mutableStateOf(null) }

    LaunchedEffect(pairedDevice) {
        pairedDevice?.let { name ->
            snackbarHostState.showSnackbar("Paired with $name!")
            navBackStackEntry?.savedStateHandle?.set("pairedDevice", null)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("adrop") },
                actions = {
                    IconButton(onClick = onNavigateDevices) {
                        Icon(Icons.Default.DevicesOther, contentDescription = "Paired devices")
                    }
                }
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        Column(
            modifier            = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            Spacer(Modifier.height(16.dp))

            // Receive window card with live countdown
            Card(
                modifier = Modifier.fillMaxWidth(),
                colors   = CardDefaults.cardColors(
                    containerColor = when {
                        receiveActive -> MaterialTheme.colorScheme.primaryContainer
                        notificationPermissionDenied -> MaterialTheme.colorScheme.errorContainer
                        else -> MaterialTheme.colorScheme.surfaceVariant
                    }
                )
            ) {
                Row(
                    modifier            = Modifier
                        .fillMaxWidth()
                        .padding(16.dp),
                    verticalAlignment   = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            receiveTitle,
                            style = MaterialTheme.typography.titleMedium,
                        )
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text  = receiveSubtitle,
                            style = MaterialTheme.typography.bodySmall,
                            color = LocalContentColor.current.copy(alpha = 0.7f),
                        )
                    }
                    Switch(
                        checked         = receiveActive,
                        enabled         = !notificationPermissionRequestInFlight,
                        onCheckedChange = { checked ->
                            if (checked) {
                                if (hasNotificationPermission(context)) {
                                    notificationPermissionDeniedEvents = 0
                                    startReceiveWindow(context)
                                } else if (!notificationPermissionRequestInFlight) {
                                    notificationPermissionRequestInFlight = true
                                    notificationPermissionLauncher.launch(
                                        Manifest.permission.POST_NOTIFICATIONS
                                    )
                                }
                            } else {
                                notificationPermissionRequestInFlight = false
                                stopReceiveWindow(context)
                            }
                        }
                    )
                }

                val progressModifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp)
                    .padding(bottom = 12.dp)
                when {
                    notificationPermissionRequestInFlight -> {
                        LinearProgressIndicator(
                            modifier = progressModifier,
                            color = MaterialTheme.colorScheme.primary,
                            trackColor = MaterialTheme.colorScheme.surfaceVariant,
                        )
                    }
                    receiveState.phase == ReceiveWindowPhase.WAITING ||
                        receiveState.phase == ReceiveWindowPhase.RESUME_WAIT -> {
                        LinearProgressIndicator(
                            progress = { countdownProgress },
                            modifier = progressModifier,
                            color = MaterialTheme.colorScheme.primary,
                            trackColor = MaterialTheme.colorScheme.surfaceVariant,
                        )
                    }
                    receiveState.phase == ReceiveWindowPhase.RECEIVING -> {
                        LinearProgressIndicator(
                            modifier = progressModifier,
                            color = MaterialTheme.colorScheme.primary,
                            trackColor = MaterialTheme.colorScheme.surfaceVariant,
                        )
                    }
                }
            }

            // Action buttons
            FilledTonalButton(
                onClick  = onNavigateSend,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Icon(Icons.Default.Send, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Send File or Clipboard")
            }

            OutlinedButton(
                onClick  = onNavigatePair,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Icon(Icons.Default.QrCodeScanner, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Pair with PC (Scan QR)")
            }

            OutlinedButton(
                onClick  = onNavigateDevices,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Icon(Icons.Default.DevicesOther, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Manage Paired Devices")
            }
        }
    }
}

private fun hasNotificationPermission(context: Context): Boolean =
    Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
        ContextCompat.checkSelfPermission(
            context,
            Manifest.permission.POST_NOTIFICATIONS,
        ) == PackageManager.PERMISSION_GRANTED

private fun openNotificationSettings(context: Context) {
    val intent = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
            .putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)
    } else {
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS)
            .setData(Uri.parse("package:${context.packageName}"))
    }
    context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

private fun formatSeconds(secondsRemaining: Int?): String =
    "${(secondsRemaining ?: ReceiveForegroundService.GRACE_SEC).coerceAtLeast(0)}s"

private fun startReceiveWindow(context: Context) {
    val intent = ReceiveForegroundService.startIntent(context)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        context.startForegroundService(intent)
    } else {
        context.startService(intent)
    }
}

private fun stopReceiveWindow(context: Context) {
    context.startService(ReceiveForegroundService.stopIntent(context))
}
