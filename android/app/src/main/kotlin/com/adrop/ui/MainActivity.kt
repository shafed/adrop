package com.adrop.ui

import android.content.Intent
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
                AdropNavGraph(deepLinkUri = intent?.data?.toString())
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Handle adrop://pair?d=... deep links arriving while app is in foreground.
        // The nav graph re-composition handles this via the navController deep link mechanism.
        setIntent(intent)
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
private fun AdropNavGraph(deepLinkUri: String?) {
    val navController = rememberNavController()

    NavHost(navController = navController, startDestination = Routes.HOME) {
        composable(Routes.HOME) { backStackEntry ->
            HomeScreen(
                onNavigateSend    = { navController.navigate(Routes.SEND) },
                onNavigateDevices = { navController.navigate(Routes.DEVICES) },
                onNavigatePair    = { navController.navigate(Routes.SCAN) },
                navBackStackEntry = backStackEntry,
            )
        }
        composable(Routes.SEND) {
            SendScreen(onBack = { navController.popBackStack() })
        }
        composable(Routes.DEVICES) {
            DevicesScreen(onBack = { navController.popBackStack() })
        }
        composable(Routes.SCAN) {
            ScanScreen(
                onPaired = { deviceName ->
                    // Stash device name so HomeScreen can show a "Paired!" confirmation snackbar.
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
