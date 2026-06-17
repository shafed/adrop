/**
 * QR scanning screen — shows a CameraX preview and uses ML Kit Barcode Scanning
 * to detect the adrop://pair?d=... QR produced by the Go daemon.
 */
package com.adrop.feature.pair

import android.util.Size
import androidx.camera.core.*
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import com.google.accompanist.permissions.*
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.Executors

@OptIn(ExperimentalPermissionsApi::class, ExperimentalMaterial3Api::class)
@Composable
fun ScanScreen(
    vm: PairViewModel = viewModel(factory = PairViewModel.factory(LocalContext.current)),
    initialPairingUri: String? = null,
    onPaired: (deviceName: String) -> Unit,
    onBack: () -> Unit,
) {
    val state by vm.state.collectAsState()
    val cameraPermission = rememberPermissionState(android.Manifest.permission.CAMERA)
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(initialPairingUri) {
        if (initialPairingUri != null) {
            vm.onQrScanned(initialPairingUri)
        } else {
            vm.startScanning()
            if (!cameraPermission.status.isGranted) {
                cameraPermission.launchPermissionRequest()
            }
        }
    }

    LaunchedEffect(cameraPermission.status.isGranted, initialPairingUri) {
        if (initialPairingUri == null && !cameraPermission.status.isGranted) {
            cameraPermission.launchPermissionRequest()
        }
    }

    // Show error in snackbar as well as the inline error panel so the user
    // always gets feedback even if the camera preview is still visible.
    LaunchedEffect(state) {
        if (state is PairState.Error) {
            val msg = (state as PairState.Error).message
            snackbarHostState.showSnackbar(
                message     = "Pairing failed: $msg",
                actionLabel = "Retry",
                duration    = SnackbarDuration.Long,
            ).let { result ->
                if (result == SnackbarResult.ActionPerformed) {
                    vm.reset()
                    vm.startScanning()
                }
            }
        }
    }

    when (val s = state) {
        is PairState.Success -> {
            LaunchedEffect(s) { onPaired(s.deviceName) }
        }
        else -> {}
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Scan Pairing QR") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = "Back"
                        )
                    }
                }
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
            contentAlignment = Alignment.Center,
        ) {
            when {
                initialPairingUri == null && !cameraPermission.status.isGranted -> {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("Camera permission is required to scan the pairing QR code.")
                        Spacer(Modifier.height(16.dp))
                        Button(onClick = { cameraPermission.launchPermissionRequest() }) {
                            Text("Grant Permission")
                        }
                    }
                }

                state is PairState.Connecting -> {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        CircularProgressIndicator()
                        Spacer(Modifier.height(16.dp))
                        Text("Connecting to PC…")
                    }
                }

                state is PairState.Error -> {
                    val err = (state as PairState.Error).message
                    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(24.dp)) {
                        Text("Pairing failed", style = MaterialTheme.typography.titleMedium)
                        Spacer(Modifier.height(8.dp))
                        Text(err, style = MaterialTheme.typography.bodySmall)
                        Spacer(Modifier.height(16.dp))
                        Button(onClick = { vm.reset(); vm.startScanning() }) { Text("Try Again") }
                    }
                }

                initialPairingUri == null -> {
                    QrCameraPreview(
                        onBarcodeDetected = { value ->
                            if (value.startsWith("adrop://pair")) {
                                vm.onQrScanned(value)
                            }
                        }
                    )
                }

                else -> Unit
            }
        }
    }
}

@Composable
private fun QrCameraPreview(onBarcodeDetected: (String) -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val executor = remember { Executors.newSingleThreadExecutor() }
    val scanner = remember { BarcodeScanning.getClient() }
    var hasDetected by remember { mutableStateOf(false) }

    DisposableEffect(Unit) {
        onDispose { executor.shutdown() }
    }

    AndroidView(
        factory = { ctx ->
            val previewView = PreviewView(ctx)

            val cameraProviderFuture = ProcessCameraProvider.getInstance(ctx)
            cameraProviderFuture.addListener({
                val cameraProvider = cameraProviderFuture.get()

                val preview = Preview.Builder().build().also {
                    it.setSurfaceProvider(previewView.surfaceProvider)
                }

                val imageAnalysis = ImageAnalysis.Builder()
                    .setTargetResolution(Size(1280, 720))
                    .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                    .build()

                imageAnalysis.setAnalyzer(executor) { imageProxy ->
                    if (hasDetected) {
                        imageProxy.close()
                        return@setAnalyzer
                    }
                    @androidx.camera.core.ExperimentalGetImage
                    val mediaImage = imageProxy.image
                    if (mediaImage != null) {
                        val image = InputImage.fromMediaImage(mediaImage, imageProxy.imageInfo.rotationDegrees)
                        scanner.process(image)
                            .addOnSuccessListener { barcodes ->
                                for (barcode in barcodes) {
                                    if (barcode.format == Barcode.FORMAT_QR_CODE) {
                                        val raw = barcode.rawValue ?: continue
                                        if (!hasDetected) {
                                            hasDetected = true
                                            onBarcodeDetected(raw)
                                        }
                                    }
                                }
                            }
                            .addOnCompleteListener { imageProxy.close() }
                    } else {
                        imageProxy.close()
                    }
                }

                cameraProvider.unbindAll()
                cameraProvider.bindToLifecycle(
                    lifecycleOwner,
                    CameraSelector.DEFAULT_BACK_CAMERA,
                    preview,
                    imageAnalysis,
                )
            }, ContextCompat.getMainExecutor(ctx))

            previewView
        },
        modifier = Modifier.fillMaxSize(),
    )
}
