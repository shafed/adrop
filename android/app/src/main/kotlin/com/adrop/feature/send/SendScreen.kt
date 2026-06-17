package com.adrop.feature.send

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
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
import com.adrop.ui.SharePayload

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendScreen(
    vm: SendViewModel = viewModel(factory = SendViewModel.factory(LocalContext.current)),
    onBack: () -> Unit,
    sharePayload: SharePayload? = null,
) {
    val state by vm.state.collectAsState()
    val clipboardManager = LocalClipboardManager.current

    // Apply share payload once on first composition.
    LaunchedEffect(sharePayload) {
        when (sharePayload) {
            is SharePayload.Files -> vm.setPickedUris(sharePayload.uris)
            is SharePayload.Text  -> vm.setClipboardText(sharePayload.text)
            null                  -> Unit
        }
    }

    // SAF file picker (still available when not coming from share sheet)
    val filePicker = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.OpenMultipleDocuments()
    ) { uris ->
        if (uris.isNotEmpty()) vm.setPickedUris(uris)
    }

    // Image picker for clipboard PNG mode
    val imagePicker = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        vm.setPickedImageUri(uri)
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
                    enabled  = !state.isSending,
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
                            Spacer(Modifier.width(8.dp))
                            Text("Sending…")
                        } else {
                            Icon(Icons.Default.Send, contentDescription = null)
                            Spacer(Modifier.width(8.dp))
                            Text("Send Files")
                        }
                    }

                    // Per-transfer progress message from Session.kt's ProgressFn callback.
                    // Shows which file is currently being sent (e.g. "Sending photo.jpg (1/3)").
                    // TODO: once Session.kt exposes byte-level progress via an extended ProgressFn,
                    // replace this text label with a LinearProgressIndicator showing 0..1 fraction.
                    state.transferProgress?.let { msg ->
                        Spacer(Modifier.height(4.dp))
                        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text  = msg,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }

            // Clipboard section
            item {
                Text("Clipboard", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))

                // MIME type selector
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment     = Alignment.CenterVertically,
                ) {
                    ClipboardMimeMode.entries.forEach { mode ->
                        FilterChip(
                            selected = state.clipboardMime == mode,
                            onClick  = { vm.setClipboardMime(mode) },
                            label    = { Text(mode.label) },
                        )
                    }
                }
                Spacer(Modifier.height(8.dp))

                when (state.clipboardMime) {
                    ClipboardMimeMode.IMAGE_PNG -> {
                        // Image picker button
                        OutlinedButton(
                            onClick  = { imagePicker.launch("image/*") },
                            modifier = Modifier.fillMaxWidth(),
                            enabled  = !state.isSending,
                        ) {
                            Icon(Icons.Default.Image, contentDescription = null)
                            Spacer(Modifier.width(8.dp))
                            Text(
                                if (state.pickedImageUri != null) "Image selected"
                                else "Pick Image"
                            )
                        }
                    }
                    else -> {
                        OutlinedTextField(
                            value         = state.clipboardText,
                            onValueChange = vm::setClipboardText,
                            label         = {
                                Text(if (state.clipboardMime == ClipboardMimeMode.HTML) "HTML" else "Clipboard text")
                            },
                            modifier      = Modifier.fillMaxWidth(),
                            minLines      = 3,
                            maxLines      = 6,
                        )
                        Spacer(Modifier.height(8.dp))
                        OutlinedButton(
                            onClick = {
                                val text = clipboardManager.getText()?.toString() ?: ""
                                vm.setClipboardText(text)
                            },
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Icon(Icons.Default.ContentPaste, contentDescription = null)
                            Spacer(Modifier.width(4.dp))
                            Text("Paste")
                        }
                    }
                }

                Spacer(Modifier.height(8.dp))
                val sendEnabled = !state.isSending && state.selectedDevice != null && when (state.clipboardMime) {
                    ClipboardMimeMode.IMAGE_PNG -> state.pickedImageUri != null
                    else -> state.clipboardText.isNotBlank()
                }
                Button(
                    onClick  = { vm.sendClipboard() },
                    enabled  = sendEnabled,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Icon(Icons.Default.Send, contentDescription = null)
                    Spacer(Modifier.width(4.dp))
                    Text("Send Clipboard")
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
