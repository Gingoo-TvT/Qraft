package main

import "testing"

func TestWindowSizeFollowsDisplayScaleAndFitsScreen(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		dpi, screenW, screenH    int
		wantW, wantH, minW, minH int
	}{
		{"100 percent", 96, 1920, 1080, 1280, 860, 850, 620},
		{"125 percent", 120, 2560, 1440, 1600, 1075, 1063, 775},
		{"150 percent", 144, 2560, 1440, 1920, 1290, 1275, 930},
		{"200 percent", 192, 3840, 2160, 2560, 1720, 1700, 1240},
		{"small scaled screen", 192, 1366, 768, 1206, 568, 1206, 568},
		{"DPI unavailable", 0, 1920, 1080, 1280, 860, 850, 620},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width, height := initialWindowSize(tc.dpi, tc.screenW, tc.screenH)
			if width != tc.wantW || height != tc.wantH {
				t.Fatalf("window=%dx%d, want %dx%d", width, height, tc.wantW, tc.wantH)
			}
			minW, minH := minimumWindowSize(tc.dpi, width, height)
			if minW != tc.minW || minH != tc.minH {
				t.Fatalf("minimum=%dx%d, want %dx%d", minW, minH, tc.minW, tc.minH)
			}
		})
	}
}
