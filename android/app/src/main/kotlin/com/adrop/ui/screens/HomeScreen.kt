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

import android.content.Context
import android.os.Build
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.navigation.NavBackStackEntry
import com.adrop.feature.receive.ReceiveForegroundService
import com.adrop.ui.ReceiveWindowState
import com.google.accompanist.permissions.ExperimentalPermissionsApi
import com.google.accompanist.permissions.isGranted
import com.google.accompanist.permissions.rememberPermissionState

@OptIn(ExperimentalPermissionsApi::class, ExperimentalMaterial3Api::class)
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

    // Notification permission (Android 13+)
    val notifPermission = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        rememberPermissionState(android.Manifest.permission.POST_NOTIFICATIONS)
    } else null

    // Snackbar for receive-window errors and pair-success confirmations.
    val snackbarHostState = remember { SnackbarHostState() }

    // Receive-window errors from ReceiveForegroundService.
    LaunchedEffect(receiveState.lastError) {
        receiveState.lastError?.let { error ->
            snackbarHostState.showSnackbar("Receive error: $error")
            ReceiveWindowState.clearError()
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
                    containerColor = if (receiveActive)
                        MaterialTheme.colorScheme.primaryContainer
                    else
                        MaterialTheme.colorScheme.surfaceVariant
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
                            if (receiveActive) "Receiving…" else "Ready to receive",
                            style = MaterialTheme.typography.titleMedium,
                        )
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text  = if (receiveActive)
                                "Open — closes when the transfer finishes"
                            else
                                "Tap to open receive window",
                            style = MaterialTheme.typography.bodySmall,
                            color = LocalContentColor.current.copy(alpha = 0.7f),
                        )
                    }
                    Switch(
                        checked         = receiveActive,
                        onCheckedChange = { checked ->
                            if (checked) {
                                // Request notification permission first on API 33+.
                                if (notifPermission != null && !notifPermission.status.isGranted) {
                                    notifPermission.launchPermissionRequest()
                                }
                                startReceiveWindow(context)
                            } else {
                                stopReceiveWindow(context)
                            }
                        }
                    )
                }

                // Indeterminate activity bar — shown while the window is open.
                if (receiveActive) {
                    LinearProgressIndicator(
                        modifier        = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 16.dp)
                            .padding(bottom = 12.dp),
                        color           = MaterialTheme.colorScheme.primary,
                        trackColor      = MaterialTheme.colorScheme.surfaceVariant,
                    )
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
