package com.adrop.ui

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.Composable
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.adrop.feature.devices.DevicesScreen
import com.adrop.feature.pair.ScanScreen
import com.adrop.feature.send.SendScreen
import com.adrop.ui.screens.HomeScreen
import com.adrop.ui.theme.AdropTheme

class MainActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            AdropTheme {
                AdropNavGraph(
                    deepLinkUri  = intent?.data?.toString(),
                    sharePayload = intent?.toSharePayload(),
                )
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }
}

// ---------------------------------------------------------------------------
// Share intent parsing
// ---------------------------------------------------------------------------

/** What the system share sheet handed us. */
sealed class SharePayload {
    /** One or more file URIs shared from another app (gallery, file manager, etc.). */
    data class Files(val uris: List<Uri>) : SharePayload()
    /** Plain text or URL shared from a browser, notes app, etc. */
    data class Text(val text: String) : SharePayload()
}

@Suppress("DEPRECATION")
private fun Intent.toSharePayload(): SharePayload? {
    return when (action) {
        Intent.ACTION_SEND -> {
            val text = getStringExtra(Intent.EXTRA_TEXT)
            if (!text.isNullOrBlank()) {
                SharePayload.Text(text)
            } else {
                val uri: Uri? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
                } else {
                    @Suppress("UNCHECKED_CAST")
                    getParcelableExtra(Intent.EXTRA_STREAM)
                }
                uri?.let { SharePayload.Files(listOf(it)) }
            }
        }
        Intent.ACTION_SEND_MULTIPLE -> {
            val uris: ArrayList<Uri>? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
            } else {
                @Suppress("UNCHECKED_CAST")
                getParcelableArrayListExtra(Intent.EXTRA_STREAM)
            }
            uris?.takeIf { it.isNotEmpty() }?.let { SharePayload.Files(it) }
        }
        else -> null
    }
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

private object Routes {
    const val HOME    = "home"
    const val SEND    = "send"
    const val DEVICES = "devices"
    const val SCAN    = "scan"
}

@Composable
private fun AdropNavGraph(deepLinkUri: String?, sharePayload: SharePayload?) {
    val navController = rememberNavController()

    // If launched from the share sheet, open SendScreen directly instead of Home.
    val startDestination = if (sharePayload != null) Routes.SEND else Routes.HOME

    NavHost(navController = navController, startDestination = startDestination) {
        composable(Routes.HOME) { backStackEntry ->
            HomeScreen(
                onNavigateSend    = { navController.navigate(Routes.SEND) },
                onNavigateDevices = { navController.navigate(Routes.DEVICES) },
                onNavigatePair    = { navController.navigate(Routes.SCAN) },
                navBackStackEntry = backStackEntry,
            )
        }
        composable(Routes.SEND) {
            SendScreen(
                onBack = {
                    if (!navController.popBackStack()) {
                        // Launched directly from share sheet — no back stack; go home.
                        navController.navigate(Routes.HOME) {
                            popUpTo(Routes.SEND) { inclusive = true }
                        }
                    }
                },
                sharePayload = sharePayload,
            )
        }
        composable(Routes.DEVICES) {
            DevicesScreen(onBack = { navController.popBackStack() })
        }
        composable(Routes.SCAN) {
            ScanScreen(
                onPaired = { deviceName ->
                    navController
                        .getBackStackEntry(Routes.HOME)
                        .savedStateHandle["pairedDevice"] = deviceName
                    navController.popBackStack(Routes.HOME, inclusive = false)
                },
                onBack   = { navController.popBackStack() },
            )
        }
    }
}
