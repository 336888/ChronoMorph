package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"unsafe"
)

// Capture only the native client area on the Windows release runner.
func captureDesktopWindow(hwnd uintptr, path string) error {
	var r winRect
	if uc("GetClientRect", hwnd, uintptr(unsafe.Pointer(&r))) == 0 {
		return fmt.Errorf("GetClientRect failed")
	}
	w, h := int(r.right), int(r.bottom)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("empty window size")
	}
	dc := uc("GetDC", hwnd)
	defer uc("ReleaseDC", hwnd, dc)
	mem := gc("CreateCompatibleDC", dc)
	defer gc("DeleteDC", mem)
	bmp := gc("CreateCompatibleBitmap", dc, uintptr(w), uintptr(h))
	defer gc("DeleteObject", bmp)
	prev := gc("SelectObject", mem, bmp)
	ok := uc("PrintWindow", hwnd, mem, 3)
	if ok == 0 {
		ok = gc("BitBlt", mem, 0, 0, uintptr(w), uintptr(h), dc, 0, 0, 0x00cc0020)
	}
	gc("SelectObject", mem, prev)
	if ok == 0 {
		return fmt.Errorf("window capture failed")
	}
	type header struct {
		size                   uint32
		width, height          int32
		planes, bits           uint16
		compression, sizeImage uint32
		x, y                   int32
		used, important        uint32
	}
	info := header{size: uint32(unsafe.Sizeof(header{})), width: int32(w), height: -int32(h), planes: 1, bits: 32}
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	if gc("GetDIBits", mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&im.Pix[0])), uintptr(unsafe.Pointer(&info)), 0) == 0 {
		return fmt.Errorf("GetDIBits failed")
	}
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+2] = im.Pix[i+2], im.Pix[i]
		im.Pix[i+3] = 255
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, im)
}
