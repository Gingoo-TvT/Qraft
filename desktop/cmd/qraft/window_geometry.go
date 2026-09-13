package main

// The WebView host uses physical pixels; these design sizes are at 96 DPI.
func scaleWindowPixels(value, dpi int) int {
	if dpi <= 0 {
		dpi = 96
	}
	return (value*dpi + 48) / 96
}

func initialWindowSize(dpi, screenWidth, screenHeight int) (int, int) {
	width, height := scaleWindowPixels(1280, dpi), scaleWindowPixels(860, dpi)
	if screenWidth > scaleWindowPixels(80, dpi) {
		width = min(width, screenWidth-scaleWindowPixels(80, dpi))
	}
	if screenHeight > scaleWindowPixels(100, dpi) {
		height = min(height, screenHeight-scaleWindowPixels(100, dpi))
	}
	return width, height
}

func minimumWindowSize(dpi, availableWidth, availableHeight int) (int, int) {
	return min(scaleWindowPixels(850, dpi), availableWidth), min(scaleWindowPixels(620, dpi), availableHeight)
}
