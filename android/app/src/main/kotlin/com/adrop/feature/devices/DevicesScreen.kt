package com.adrop.feature.devices

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.adrop.data.trust.TrustedDevice
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DevicesScreen(
    vm: DevicesViewModel = viewModel(factory = DevicesViewModel.factory(LocalContext.current)),
    onBack: () -> Unit,
    onNavigatePair: () -> Unit,
) {
    val devices by vm.devices.collectAsState()
    val snackbarHostState = remember { SnackbarHostState() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Paired Devices") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        if (devices.isEmpty()) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .padding(24.dp),
            ) {
                Column {
                    Text(
                        "No paired devices yet.",
                        style = MaterialTheme.typography.titleMedium,
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "Scan the QR code shown by your PC to pair.",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Spacer(Modifier.height(16.dp))
                    Button(onClick = onNavigatePair) {
                        Icon(Icons.Default.QrCodeScanner, contentDescription = null)
                        Spacer(Modifier.width(8.dp))
                        Text("Scan QR")
                    }
                }
            }
        } else {
            LazyColumn(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding),
                contentPadding = PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                items(devices, key = { it.id }) { device ->
                    DeviceCard(
                        device   = device,
                        onRevoke = {
                            vm.revoke(device)
                            // Show confirmation via snackbar (LaunchedEffect not needed here —
                            // we can call showSnackbar in a coroutine from a lambda safely via
                            // the provided CoroutineScope from the Scaffold).
                        },
                        snackbarHostState = snackbarHostState,
                    )
                }
            }
        }
    }
}

@Composable
private fun DeviceCard(
    device: TrustedDevice,
    onRevoke: () -> Unit,
    snackbarHostState: SnackbarHostState,
) {
    var showConfirm by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    if (showConfirm) {
        AlertDialog(
            onDismissRequest = { showConfirm = false },
            title            = { Text("Remove ${device.name}?") },
            text             = { Text("This device will no longer be trusted. You will need to pair again to send or receive files.") },
            confirmButton    = {
                TextButton(onClick = {
                    onRevoke()
                    showConfirm = false
                    scope.launch {
                        snackbarHostState.showSnackbar("${device.name} removed")
                    }
                }) { Text("Remove") }
            },
            dismissButton    = {
                TextButton(onClick = { showConfirm = false }) { Text("Cancel") }
            },
        )
    }

    Card(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text(device.name, style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(4.dp))
                Text(device.addr, style = MaterialTheme.typography.bodySmall)
                Text(
                    "Paired ${formatDate(device.pairedAt)}",
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(
                    "FP: ${device.fingerprint.take(16)}…",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            IconButton(onClick = { showConfirm = true }) {
                Icon(Icons.Default.Delete, contentDescription = "Revoke")
            }
        }
    }
}

private fun formatDate(epochMillis: Long): String {
    val sdf = SimpleDateFormat("yyyy-MM-dd", Locale.getDefault())
    return sdf.format(Date(epochMillis))
}
