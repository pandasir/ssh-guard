package desktopui

import (
	"github.com/egoist/mygo/ui"
)

// Midnight navy & luminous blue design system tokens matching the redesigned UI.
var (
	colCanvasBg        = ui.Hex("#070a0f")
	colSidebarBg       = ui.Hex("#0d131d")
	colTopBarBg        = ui.Hex("#0f1520")
	colTabStripBg      = ui.Hex("#0a0f17")
	colCardBg          = ui.Hex("#111824")
	colCardBgHover     = ui.Hex("#151e2d")
	colModalBg         = ui.Hex("#101723")
	colInputBg         = ui.Hex("#090e16")
	colSurfaceBtn      = ui.Hex("#192334")
	colSurfaceBtnHover = ui.Hex("#223047")
	colSurfaceBtnPress = ui.Hex("#141c2a")

	colBorder       = ui.Hex("#1c2637")
	colBorderStrong = ui.Hex("#28364b")
	colDivider      = ui.Hex("#182231")

	colTextPrimary   = ui.Hex("#f1f5f9")
	colTextSecondary = ui.Hex("#cbd5e1")
	colTextMuted     = ui.Hex("#7b8ba1")
	colTextDim       = ui.Hex("#5d6d83")

	colAccent      = ui.Hex("#2f73f5")
	colAccentLight = ui.Hex("#4d8ff8")
	colAccentDark  = ui.Hex("#1f5bd4")
	colAccentText  = ui.Hex("#79b8ff")

	colSuccess       = ui.Hex("#34d399")
	colSuccessBg     = ui.RGBA(52, 211, 153, 0.13)
	colSuccessBorder = ui.RGBA(52, 211, 153, 0.36)

	colWarn       = ui.Hex("#e5a93c")
	colWarnBg     = ui.RGBA(217, 159, 46, 0.12)
	colWarnBorder = ui.RGBA(217, 159, 46, 0.36)

	colDanger       = ui.Hex("#f87171")
	colDangerDot    = ui.Hex("#ef4444")
	colDangerBg     = ui.RGBA(239, 68, 68, 0.14)
	colDangerBgHov  = ui.RGBA(239, 68, 68, 0.24)
	colDangerBorder = ui.RGBA(239, 68, 68, 0.38)
)

// Static vector icons parsed once at package initialization (zero per-frame allocation).
var (
	iconFingerprintSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 10a2 2 0 0 0-2 2c0 1.02-.1 2.51-.26 4"/><path d="M14 13.12c0 2.38 0 6.38-1 8.88"/><path d="M17.29 21.02c.12-.6.43-2.3.5-3.02"/><path d="M2 12a10 10 0 0 1 18-6"/><path d="M2 16h.01"/><path d="M21.8 16c.2-2 .131-5.354 0-6"/><path d="M5 19.5C5.5 18 6 15 6 12a6 6 0 0 1 .34-2"/><path d="M8.65 22c.21-.66.45-1.32.57-2"/><path d="M9 6.8a6 6 0 0 1 9 5.2v2"/></svg>`))

	iconVideoCamSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="6" width="14" height="12" rx="2.5"/><path d="m16 10 4.55-2.73A1 1 0 0 1 22 8.13v7.74a1 1 0 0 1-1.45.86L16 14"/></svg>`))

	iconAuditDocSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M8 13h8"/><path d="M8 17h6"/><path d="M8 9h2"/></svg>`))

	iconDatabaseSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3"/></svg>`))

	iconLockSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="11" width="16" height="10" rx="2.5"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/><circle cx="12" cy="16" r="1.2" fill="currentColor" stroke="none"/></svg>`))

	iconShieldLockSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><rect x="9" y="11" width="6" height="5" rx="1"/><path d="M10.5 11V9.5a1.5 1.5 0 0 1 3 0V11"/></svg>`))

	// Luminous vector ">_" prompt emblem rendered inside the hero glass tile.
	iconHeroPromptSVG = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 80 80" fill="none">
		<defs>
			<linearGradient id="promptGrad" x1="12" y1="16" x2="68" y2="64" gradientUnits="userSpaceOnUse">
				<stop offset="0%" stop-color="#8ec5ff"/>
				<stop offset="55%" stop-color="#4e94ff"/>
				<stop offset="100%" stop-color="#2c71f0"/>
			</linearGradient>
		</defs>
		<path d="M17 22L37 40L17 58" stroke="#3b82f6" stroke-opacity="0.28" stroke-width="12" stroke-linecap="round" stroke-linejoin="round"/>
		<line x1="41" y1="57" x2="64" y2="57" stroke="#3b82f6" stroke-opacity="0.28" stroke-width="11" stroke-linecap="round"/>
		<path d="M17 22L37 40L17 58" stroke="url(#promptGrad)" stroke-width="7.5" stroke-linecap="round" stroke-linejoin="round"/>
		<line x1="41" y1="57" x2="64" y2="57" stroke="url(#promptGrad)" stroke-width="7.5" stroke-linecap="round"/>
	</svg>`))
)

// glowPrimaryButton renders a vibrant royal-blue gradient button with subtle specular border and glow.
func glowPrimaryButton(c *ui.Context, label string, padV, padH float32) ui.Element {
	btn := ui.ButtonBase(c).Padding(padV, padH).Radius(7).Cursor(ui.CursorPointer)
	from, to := colAccentLight, colAccentDark
	border := ui.RGBA(147, 197, 253, 0.36)
	glow := ui.RGBA(37, 99, 235, 0.42)
	if btn.Pressed() {
		from, to = colAccent, ui.Hex("#1a4db8")
		glow = ui.RGBA(37, 99, 235, 0.25)
	} else if btn.Hovered() {
		from, to = ui.Hex("#5e9bfa"), ui.Hex("#2867e5")
		border = ui.RGBA(191, 219, 254, 0.5)
		glow = ui.RGBA(59, 130, 246, 0.56)
	}
	btn.LinearGradient(ui.LinearGradient{From: from, To: to, Angle: 180}).
		Border(1, border).
		InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.22)).
		Shadow(0, 4, 14, 0, glow).
		Children(func() {
			ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(ui.Hex("#ffffff")).SingleLine()
		})
	return btn
}

// subtleButton renders a clean dark-slate secondary action button.
func subtleButton(c *ui.Context, label string, padV, padH float32) ui.Element {
	btn := ui.ButtonBase(c).Padding(padV, padH).Radius(7).Cursor(ui.CursorPointer)
	bg := colSurfaceBtn
	border := colBorderStrong
	txt := colTextSecondary
	if btn.Pressed() {
		bg = colSurfaceBtnPress
	} else if btn.Hovered() {
		bg = colSurfaceBtnHover
		border = ui.Hex("#354761")
		txt = colTextPrimary
	}
	btn.Background(bg).Border(1, border).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(txt).SingleLine()
	})
	return btn
}

// dangerActionButton renders a subtle crimson-tinted destructive button.
func dangerActionButton(c *ui.Context, label string, padV, padH float32) ui.Element {
	btn := ui.ButtonBase(c).Padding(padV, padH).Radius(7).Cursor(ui.CursorPointer)
	bg := colDangerBg
	border := colDangerBorder
	txt := colDanger
	if btn.Pressed() {
		bg = ui.RGBA(239, 68, 68, 0.28)
	} else if btn.Hovered() {
		bg = colDangerBgHov
		border = ui.RGBA(248, 113, 113, 0.55)
		txt = ui.Hex("#fca5a5")
	}
	btn.Background(bg).Border(1, border).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(txt).SingleLine()
	})
	return btn
}

// sidebarFooterItem renders one vertical icon+label navigation item in the sidebar footer.
func sidebarFooterItem(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	btn := ui.ButtonBase(c).Grow(1).Padding(7, 4).Radius(8).Cursor(ui.CursorPointer)
	fg := colTextMuted
	if btn.Pressed() {
		btn.Background(ui.RGBA(148, 163, 184, 0.14))
		fg = colTextPrimary
	} else if btn.Hovered() {
		btn.Background(ui.RGBA(148, 163, 184, 0.08))
		fg = colTextSecondary
	}
	btn.Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Box(c).Size(24, 20).Center().Children(func() {
				ui.Icon(c, icon).Size(18, 18).TextColor(fg)
			})
			ui.Text(c, label).FontSize(11).TextColor(fg).SingleLine()
		})
	})
	return btn
}

// styleModalPanel applies the unified dark elevated dialog panel styling.
func styleModalPanel(backdrop, panel ui.Element, w float32) {
	backdrop.Background(ui.RGBA(3, 6, 12, 0.76))
	panel.Width(w).Padding(22).Gap(12).Radius(12).
		Background(colModalBg).Border(1, colBorderStrong).
		Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.65))
}
