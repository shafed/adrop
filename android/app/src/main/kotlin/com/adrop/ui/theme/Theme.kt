package com.adrop.ui.theme

import android.app.Activity
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

private val LightColors = lightColorScheme(
    primary          = Color(0xFF006874),
    onPrimary        = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFF97F0FF),
    secondary        = Color(0xFF4A6267),
    surface          = Color(0xFFFAFDFD),
    background       = Color(0xFFFAFDFD),
)

private val DarkColors = darkColorScheme(
    primary          = Color(0xFF4FD8EB),
    onPrimary        = Color(0xFF00363D),
    primaryContainer = Color(0xFF004F58),
    secondary        = Color(0xFFB0CBD0),
    surface          = Color(0xFF191C1D),
    background       = Color(0xFF191C1D),
)

@Composable
fun AdropTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    dynamicColor: Boolean = true,
    content: @Composable () -> Unit,
) {
    val colorScheme = when {
        dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val context = LocalContext.current
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        }
        darkTheme -> DarkColors
        else      -> LightColors
    }

    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            WindowCompat.getInsetsController(window, view).isAppearanceLightStatusBars = !darkTheme
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        typography  = Typography(),
        content     = content,
    )
}
