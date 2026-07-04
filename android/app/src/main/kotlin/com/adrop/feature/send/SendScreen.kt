package com.adrop.feature.send

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
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
import androidx.compose.ui.text.style.TextOverflow
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
    onNavigatePair: () -> Unit = {},
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
                is SendResult.Queued ->
                    snackbarHostState.showSnackbar("PC is offline — queued, will send when it's back")
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
                    EmptyPairedDevices(onNavigatePair = onNavigatePair)
                } else {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        state.devices.forEach { device ->
                            DeviceOption(
                                device = device,
                                selected = state.selectedDevice?.fingerprint == device.fingerprint,
                                onClick  = { vm.selectDevice(device) },
                            )
                        }
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
                        if (state.isSending && state.sendPhase != null) {
                            CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                            Spacer(Modifier.width(8.dp))
                            Text(if (state.sendPhase == SendPhase.PREPARING) "Preparing…" else "Sending…")
                        } else {
                            Icon(Icons.Default.Send, contentDescription = null)
                            Spacer(Modifier.width(8.dp))
                            Text("Send Files")
                        }
                    }

                    // Send status from preparation/hashing through Session.kt's ProgressFn callback.
                    state.transferProgress?.takeIf { state.sendPhase != null }?.let { msg ->
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
private fun EmptyPairedDevices(onNavigatePair: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            text = "No paired devices yet.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedButton(
            onClick = onNavigatePair,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Icon(Icons.Default.Add, contentDescription = null)
            Spacer(Modifier.width(8.dp))
            Text("Pair Device")
        }
    }
}

@Composable
private fun DeviceOption(
    device: TrustedDevice,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val borderColor = if (selected) {
        MaterialTheme.colorScheme.primary
    } else {
        MaterialTheme.colorScheme.outlineVariant
    }
    val containerColor = if (selected) {
        MaterialTheme.colorScheme.primaryContainer
    } else {
        MaterialTheme.colorScheme.surface
    }

    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
        shape = MaterialTheme.shapes.medium,
        color = containerColor,
        border = BorderStroke(1.dp, borderColor),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RadioButton(selected = selected, onClick = null)
            Spacer(Modifier.width(8.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = device.name,
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = device.addr,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = "Fingerprint …${device.fingerprintSuffix()}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

private fun TrustedDevice.fingerprintSuffix(): String =
    fingerprint
        .takeLast(12)
        .chunked(4)
        .joinToString(" ")
