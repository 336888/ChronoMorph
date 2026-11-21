package main

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	nUser       = windows.NewLazySystemDLL("user32.dll")
	nGdi        = windows.NewLazySystemDLL("gdi32.dll")
	nKernel     = windows.NewLazySystemDLL("kernel32.dll")
	desktopProc = syscall.NewCallback(desktopWindowProc)
)

//go:generate go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico assets/tray.ico -o icon_windows_amd64.syso

const desktopClass = "ChronoMorph.Native.Desktop.1"
const msgShow = 0x8001
const msgResult = 0x8002
const msgQuit = 0x8003
const desktopStyle = 0x02CA0000 // caption, system menu, minimize button, clip children
const screenW = 844
const screenH = 546

type winRect struct{ left, top, right, bottom int32 }
type winPoint struct{ x, y int32 }
type winMsg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	timestamp      uint32
	point          winPoint
	private        uint32
}
type paintInfo struct {
	dc              uintptr
	erase           int32
	rect            winRect
	restore, update int32
	spare           [32]byte
}
type winClass struct {
	size, style                        uint32
	proc                               uintptr
	classExtra, winExtra               int32
	instance, icon, cursor, background uintptr
	menu, class                        *uint16
	smallIcon                          uintptr
}
type drawItem struct {
	typ, id, item, action, state uint32
	wnd, dc                      uintptr
	rect                         winRect
	data                         uintptr
}

func utf(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

//go:uintptrescapes
func uc(name string, args ...uintptr) uintptr { r, _, _ := nUser.NewProc(name).Call(args...); return r }

//go:uintptrescapes
func gc(name string, args ...uintptr) uintptr { r, _, _ := nGdi.NewProc(name).Call(args...); return r }

//go:uintptrescapes
func kc(name string, args ...uintptr) uintptr {
	r, _, _ := nKernel.NewProc(name).Call(args...)
	return r
}
func colorRef(rgb int) uintptr { return uintptr((rgb&255)<<16 | rgb&0xff00 | (rgb>>16)&255) }
func setText(w uintptr, s string) {
	p := utf(s)
	uc("SetWindowTextW", w, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}
func getText(w uintptr) string {
	n := uc("GetWindowTextLengthW", w)
	b := make([]uint16, n+1)
	uc("GetWindowTextW", w, uintptr(unsafe.Pointer(&b[0])), n+1)
	return windows.UTF16ToString(b)
}
func notifyWindow(w uintptr, msg uint32) { uc("PostMessageW", w, uintptr(msg), 0, 0) }
func existingDesktop() uintptr {
	name := utf(desktopClass)
	w := uc("FindWindowW", uintptr(unsafe.Pointer(name)), 0)
	runtime.KeepAlive(name)
	return w
}
func (u *desktopUI) px(n int) int { return int(math.Round(float64(n) * u.scale)) }

// Compress the navigation rail while keeping editor widths and font sizes intact.
func layoutX(x int) int {
	if x >= 187 {
		return x - 34
	}
	return x * 153 / 187
}
func (u *desktopUI) rect(x, y, w, h int) winRect {
	return winRect{int32(u.px(layoutX(x))), int32(u.px(y)), int32(u.px(layoutX(x + w))), int32(u.px(y + h))}
}
func (u *desktopUI) font(size, weight int) uintptr {
	k := [2]int{size, weight}
	if f := u.fonts[k]; f != 0 {
		return f
	}
	family := utf("Microsoft YaHei UI")
	f := gc("CreateFontW", uintptr(-u.px(size)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(family)))
	runtime.KeepAlive(family)
	u.fonts[k] = f
	return f
}
func (u *desktopUI) text(dc uintptr, x, y, w, h, size, weight, rgb int, s string) {
	drawText(dc, u.rect(x, y, w, h), u.font(size, weight), rgb, u.tr(s), 0x8824) // single line, vertical center, ellipsis, literal ampersands
}
func drawText(dc uintptr, rect winRect, font uintptr, rgb int, s string, flags uintptr) {
	if s == "" {
		return
	}
	p := utf(s)
	prev := gc("SelectObject", dc, font)
	gc("SetBkMode", dc, 1)
	gc("SetTextColor", dc, colorRef(rgb))
	uc("DrawTextW", dc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&rect)), flags)
	gc("SelectObject", dc, prev)
	runtime.KeepAlive(p)
}
func fillRect(dc uintptr, rect winRect, rgb int) {
	brush := gc("CreateSolidBrush", colorRef(rgb))
	uc("FillRect", dc, uintptr(unsafe.Pointer(&rect)), brush)
	gc("DeleteObject", brush)
}
func rounded(dc uintptr, r winRect, radius int, fill, border int) {
	br := gc("CreateSolidBrush", colorRef(fill))
	pen := gc("CreatePen", 0, 1, colorRef(border))
	b, p := gc("SelectObject", dc, br), gc("SelectObject", dc, pen)
	gc("RoundRect", dc, uintptr(r.left), uintptr(r.top), uintptr(r.right), uintptr(r.bottom), uintptr(radius*2), uintptr(radius*2))
	gc("SelectObject", dc, p)
	gc("SelectObject", dc, b)
	gc("DeleteObject", pen)
	gc("DeleteObject", br)
}
func drawLine(dc uintptr, x1, y1, x2, y2, width, rgb int) {
	p := gc("CreatePen", 0, uintptr(width), colorRef(rgb))
	old := gc("SelectObject", dc, p)
	gc("MoveToEx", dc, uintptr(x1), uintptr(y1), 0)
	gc("LineTo", dc, uintptr(x2), uintptr(y2))
	gc("SelectObject", dc, old)
	gc("DeleteObject", p)
}

func createControl(class, label string, ex, style uintptr, r winRect, parent, id, instance uintptr) uintptr {
	a, b := utf(class), utf(label)
	h := uc("CreateWindowExW", ex, uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), style, uintptr(r.left), uintptr(r.top), uintptr(r.right-r.left), uintptr(r.bottom-r.top), parent, id, instance, 0)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	return h
}

func setDesktopDPI() {
	proc := nUser.NewProc("SetProcessDpiAwarenessContext")
	if proc.Find() == nil {
		proc.Call(^uintptr(1))
	} // system-DPI aware
}
func (u *desktopUI) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u.instance = kc("GetModuleHandleW", 0)
	dc := uc("GetDC", 0)
	u.scale = float64(gc("GetDeviceCaps", dc, 88)) / 96
	uc("ReleaseDC", 0, dc)
	var area winRect
	uc("SystemParametersInfoW", 48, 0, uintptr(unsafe.Pointer(&area)), 0)
	if area.right <= area.left || area.bottom <= area.top {
		area = winRect{0, 0, 1280, 720}
	}
	u.scale = math.Min(u.scale, math.Min(float64(area.right-area.left-20)/float64(layoutX(screenW)), float64(area.bottom-area.top-58)/screenH))
	if u.scale <= 0 {
		u.scale = 1
	}
	name := utf(desktopClass)
	icon := uc("LoadIconW", u.instance, 1)
	cursor := uc("LoadCursorW", 0, 32512)
	wc := winClass{size: uint32(unsafe.Sizeof(winClass{})), proc: desktopProc, instance: u.instance, icon: icon, smallIcon: icon, cursor: cursor, class: name}
	if uc("RegisterClassExW", uintptr(unsafe.Pointer(&wc))) == 0 {
		ready <- fmt.Errorf("无法注册桌面窗口")
		return
	}
	activeDesktop = u
	r := u.rect(0, 0, screenW, screenH)
	uc("AdjustWindowRectEx", uintptr(unsafe.Pointer(&r)), desktopStyle, 0, 0)
	w, h := r.right-r.left, r.bottom-r.top
	x := area.left + (area.right-area.left-w)/2
	y := area.top + (area.bottom-area.top-h)/2
	u.hwnd = createControl(desktopClass, "Slacker", 0, desktopStyle, winRect{x, y, x + w, y + h}, 0, 0, u.instance)
	if u.hwnd == 0 {
		ready <- fmt.Errorf("无法创建桌面窗口")
		return
	}
	// Use a dark title bar and native rounded corners when DWM supports them.
	dark := int32(1)
	corner := int32(2)
	dwm := windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	if dwm.Find() == nil {
		dwm.Call(u.hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
		dwm.Call(u.hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	}
	u.icon = uc("LoadImageW", u.instance, 1, 1, uintptr(u.px(48)), uintptr(u.px(48)), 0x8000)
	if u.icon == 0 {
		u.icon = icon
	}
	u.inputBrush = gc("CreateSolidBrush", colorRef(editorBG))
	u.backgroundBrush = gc("CreateSolidBrush", colorRef(bg))
	u.setupControls()
	u.ctrl.mu.Lock()
	language := u.ctrl.cfg.networkOptions().Language
	u.ctrl.mu.Unlock()
	u.setLanguage(language)
	u.setPage(homePage)
	u.refresh()
	uc("SetTimer", u.hwnd, 1, 1000, 0)
	uc("ShowWindow", u.hwnd, 5)
	uc("UpdateWindow", u.hwnd)
	ready <- nil
	var msg winMsg
	for {
		ret := int32(uc("GetMessageW", uintptr(unsafe.Pointer(&msg)), 0, 0, 0))
		if ret <= 0 {
			break
		}
		if uc("IsDialogMessageW", u.hwnd, uintptr(unsafe.Pointer(&msg))) == 0 {
			uc("TranslateMessage", uintptr(unsafe.Pointer(&msg)))
			uc("DispatchMessageW", uintptr(unsafe.Pointer(&msg)))
		}
	}
	for _, f := range u.fonts {
		gc("DeleteObject", f)
	}
	gc("DeleteObject", u.inputBrush)
	gc("DeleteObject", u.backgroundBrush)
	uc("UnregisterClassW", uintptr(unsafe.Pointer(name)), u.instance)
	runtime.KeepAlive(name)
}
func desktopWindowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	u := activeDesktop
	if u != nil {
		switch msg {
		case 0x0014:
			return 1 // paint background in the back buffer
		case 0x0318:
			u.paint(w)
			return 0 // WM_PRINTCLIENT
		case 0x000f:
			var ps paintInfo
			dc := uc("BeginPaint", hwnd, uintptr(unsafe.Pointer(&ps)))
			mem := gc("CreateCompatibleDC", dc)
			bmp := gc("CreateCompatibleBitmap", dc, uintptr(u.px(screenW)), uintptr(u.px(screenH)))
			old := gc("SelectObject", mem, bmp)
			u.paint(mem)
			gc("BitBlt", dc, 0, 0, uintptr(u.px(screenW)), uintptr(u.px(screenH)), mem, 0, 0, 0x00CC0020)
			gc("SelectObject", mem, old)
			gc("DeleteObject", bmp)
			gc("DeleteDC", mem)
			uc("EndPaint", hwnd, uintptr(unsafe.Pointer(&ps)))
			return 0
		case 0x002b:
			var item drawItem
			kc("RtlMoveMemory", uintptr(unsafe.Pointer(&item)), l, unsafe.Sizeof(item))
			if item.typ == 4 {
				u.paintButton(&item)
				return 1
			}
		case 0x0133, 0x0134, 0x0138:
			if notice := u.controls[noticeControl]; notice != nil && l == notice.h {
				ink := muted
				if u.message != "" {
					ink = mint
				}
				if u.isError {
					ink = 0xffa2a2
				}
				gc("SetTextColor", w, colorRef(ink))
				gc("SetBkColor", w, colorRef(bg))
				return u.backgroundBrush
			}
			gc("SetTextColor", w, colorRef(fg))
			gc("SetBkColor", w, colorRef(editorBG))
			return u.inputBrush
		case 0x0111:
			if int(w&65535) == flowList && w>>16 == 1 && u.page == livePage {
				u.updateFlowDetail()
				return 0
			}
			if l != 0 && w>>16 == 0 {
				u.command(int(w & 65535))
				return 0
			}
		case 0x0113:
			if uc("IsWindowVisible", hwnd) != 0 {
				u.refresh()
			} else {
				u.ctrl.mu.Lock()
				scheduled := u.ctrl.cfg.networkOptions().Startup >= 0
				u.ctrl.mu.Unlock()
				if scheduled {
					u.refresh()
				}
			}
			return 0
		case 0x0112:
			if w&0xfff0 == 0xf020 {
				uc("ShowWindow", hwnd, 0)
				return 0
			}
		case 0x0010:
			uc("ShowWindow", hwnd, 0)
			return 0
		case msgShow:
			uc("ShowWindow", hwnd, 9)
			uc("SetForegroundWindow", hwnd)
			u.refresh()
			return 0
		case msgResult:
			u.finish()
			return 0
		case msgQuit:
			uc("DestroyWindow", hwnd)
			return 0
		case 0x0002:
			uc("PostQuitMessage", 0)
			return 0
		}
	}
	return uc("DefWindowProcW", hwnd, uintptr(msg), w, l)
}
