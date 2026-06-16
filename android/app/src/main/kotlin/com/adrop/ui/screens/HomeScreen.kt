/**
 * Home screen — the app's main entry point.
 *
 * Shows:
 *   - "Open to receive" toggle (starts/stops the foreground service)
 *   - Quick-access buttons: Send, Devices, Pair
 */
package com.adrop.ui.screens

import android.app.Activity
import android.content.Context
import android.content.Intent
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
import com.adrop.feature.receive.ReceiveForegroundService
import com.google.accompanist.permissions.ExperimentalPermissionsApi
import com.google.accompanist.permissions.isGranted
import com.google.accompanist.permissions.rememberPermissionState

@OptIn(ExperimentalPermissionsApi::class, ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    onNavigateSend:    () -> Unit,
    onNavigateDevices: () -> Unit,
    onNavigatePair:    () -> Unit,
) {
    val context = LocalContext.current
    var receiveActive by remember { mutableStateOf(false) }

    // Notification permission (Android 13+)
    val notifPermission = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        rememberPermissionState(android.Manifest.permission.POST_NOTIFICATIONS)
    } else null

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
        }
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

            // Receive window toggle
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
                    Column {
                        Text(
                            if (receiveActive) "Receiving…" else "Ready to receive",
                            style = MaterialTheme.typography.titleMedium,
                        )
                        Text(
                            if (receiveActive) "Listening for 5 min" else "Tap to open receive window",
                            style = MaterialTheme.typography.bodySmall,
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
                                receiveActive = true
                            } else {
                                stopReceiveWindow(context)
                                receiveActive = false
                            }
                        }
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
