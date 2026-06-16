package com.adrop.feature.send

import android.app.Activity
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.adrop.data.trust.TrustedDevice

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendScreen(
    vm: SendViewModel = viewModel(factory = SendViewModel.factory(LocalContext.current)),
    onBack: () -> Unit,
) {
    val state by vm.state.collectAsState()
    val clipboardManager = LocalClipboardManager.current

    // SAF file picker
    val filePicker = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.OpenMultipleDocuments()
    ) { uris ->
        if (uris.isNotEmpty()) vm.setPickedUris(uris)
    }

    // Result snackbar
    val snackbarHostState = remember { SnackbarHostState() }
    LaunchedEffect(state.result) {
        state.result?.let { result ->
            when (result) {
                is SendResult.Success ->
                    snackbarHostState.showSnackbar("Sent successfully!")
                is SendResult.Error ->
                    snackbarHostState.showSnackbar("Error: ${result.message}")
            }
            vm.clearResult()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Send") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                }
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
            contentPadding = PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            // Device selection
            item {
                Text("Target Device", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                if (state.devices.isEmpty()) {
                    Text("No paired devices. Go to Devices to pair.", style = MaterialTheme.typography.bodySmall)
                } else {
                    state.devices.forEach { device ->
                        DeviceChip(
                            device = device,
                            selected = state.selectedDevice == device,
                            onClick  = { vm.selectDevice(device) },
                        )
                    }
                }
            }

            // File section
            item {
                Text("Files", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                OutlinedButton(
                    onClick = { filePicker.launch(arrayOf("*/*")) },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Icon(Icons.Default.AttachFile, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text(
                        if (state.pickedUris.isEmpty()) "Pick Files"
                        else "${state.pickedUris.size} file(s) selected"
                    )
                }
                if (state.pickedUris.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    Button(
                        onClick  = { vm.sendFiles() },
                        enabled  = !state.isSending && state.selectedDevice != null,
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        if (state.isSending) {
                            CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                        } else {
                            Icon(Icons.Default.Send, contentDescription = null)
                            Spacer(Modifier.width(8.dp))
                            Text("Send Files")
                        }
                    }
                }
            }

            // Clipboard section
            item {
                Text("Clipboard", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(
                    value         = state.clipboardText,
                    onValueChange = vm::setClipboardText,
                    label         = { Text("Clipboard text") },
                    modifier      = Modifier.fillMaxWidth(),
                    minLines      = 3,
                    maxLines      = 6,
                )
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(
                        onClick = {
                            val text = clipboardManager.getText()?.toString() ?: ""
                            vm.setClipboardText(text)
                        },
                        modifier = Modifier.weight(1f),
                    ) {
                        Icon(Icons.Default.ContentPaste, contentDescription = null)
                        Spacer(Modifier.width(4.dp))
                        Text("Paste")
                    }
                    Button(
                        onClick  = { vm.sendClipboard() },
                        enabled  = !state.isSending && state.selectedDevice != null && state.clipboardText.isNotBlank(),
                        modifier = Modifier.weight(1f),
                    ) {
                        Icon(Icons.Default.Send, contentDescription = null)
                        Spacer(Modifier.width(4.dp))
                        Text("Send")
                    }
                }
            }
        }
    }
}

@Composable
private fun DeviceChip(
    device: TrustedDevice,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = if (selected) {
        FilterChipDefaults.filterChipColors(
            selectedContainerColor = MaterialTheme.colorScheme.primaryContainer
        )
    } else {
        FilterChipDefaults.filterChipColors()
    }
    FilterChip(
        selected = selected,
        onClick  = onClick,
        label    = { Text(device.name) },
        colors   = colors,
    )
    Spacer(Modifier.width(8.dp))
}
