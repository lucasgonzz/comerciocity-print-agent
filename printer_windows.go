package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Todo este archivo le habla al spooler de Windows por winspool.drv.
//
// 🔴 Por que el spooler y no USB directo: en Windows el driver usbprint.sys reclama la impresora
// en exclusiva, asi que WebUSB (y cualquier acceso USB crudo) no la puede abrir sin reemplazar el
// driver con Zadig -- lo que ademas dejaria la impresora inutilizable para cualquier otro programa.
// El spooler es el camino soportado, ya tiene el driver instalado, y con el tipo de datos "RAW"
// pasa los bytes ESC/POS tal cual, sin renderizar nada. Es exactamente lo que hace QZ Tray.
var (
	winspool = syscall.NewLazyDLL("winspool.drv")

	procEnumPrintersW    = winspool.NewProc("EnumPrintersW")
	procOpenPrinterW     = winspool.NewProc("OpenPrinterW")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procStartDocPrinterW = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
)

const (
	// Impresoras instaladas en esta maquina, mas las compartidas a las que esta conectada.
	printerEnumLocal       = 0x00000002
	printerEnumConnections = 0x00000004

	errorInsufficientBuffer = 122
)

// printerInfo4 es PRINTER_INFO_4W. Es el nivel mas barato de EnumPrinters: no consulta al driver
// ni al puerto, solo devuelve el nombre. Con niveles mas altos, una impresora de red apagada puede
// hacer que la enumeracion tarde varios segundos.
type printerInfo4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

// docInfo1 es DOC_INFO_1W. El Datatype "RAW" es lo que hace que el spooler no interprete el
// contenido y lo mande tal cual a la impresora.
type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

// ListarImpresoras devuelve los nombres de las impresoras que ve este equipo, tal como los muestra
// Windows. Son los mismos nombres que despues elige el operador en el sistema.
func ListarImpresoras() ([]string, error) {
	flags := uintptr(printerEnumLocal | printerEnumConnections)
	nivel := uintptr(4)

	// Primera llamada con buffer vacio: Windows contesta cuanto necesita.
	var necesarios, devueltos uint32

	ret, _, err := procEnumPrintersW.Call(
		flags, 0, nivel, 0, 0,
		uintptr(unsafe.Pointer(&necesarios)),
		uintptr(unsafe.Pointer(&devueltos)),
	)

	if ret == 0 {
		if errno, ok := err.(syscall.Errno); !ok || uintptr(errno) != errorInsufficientBuffer {
			return nil, fmt.Errorf("no se pudieron listar las impresoras: %v", err)
		}
	}

	if necesarios == 0 {
		return []string{}, nil
	}

	buffer := make([]byte, necesarios)

	ret, _, err = procEnumPrintersW.Call(
		flags, 0, nivel,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(necesarios),
		uintptr(unsafe.Pointer(&necesarios)),
		uintptr(unsafe.Pointer(&devueltos)),
	)

	if ret == 0 {
		return nil, fmt.Errorf("no se pudieron listar las impresoras: %v", err)
	}

	impresoras := make([]string, 0, devueltos)

	for i := uint32(0); i < devueltos; i++ {
		info := (*printerInfo4)(unsafe.Pointer(&buffer[uintptr(i)*unsafe.Sizeof(printerInfo4{})]))

		if info.PrinterName == nil {
			continue
		}

		nombre := utf16PunteroAString(info.PrinterName)
		if nombre != "" {
			impresoras = append(impresoras, nombre)
		}
	}

	return impresoras, nil
}

// ImprimirRaw manda los bytes tal cual a la impresora indicada.
//
// `contenido` son bytes ESC/POS ya armados por el sistema: comandos de negrita, corte de papel y
// texto. Este agente no los interpreta ni los modifica.
func ImprimirRaw(nombreImpresora string, contenido []byte) error {
	if len(contenido) == 0 {
		return errors.New("el ticket llego vacio")
	}

	nombreUTF16, err := syscall.UTF16PtrFromString(nombreImpresora)
	if err != nil {
		return err
	}

	var handle syscall.Handle

	ret, _, err := procOpenPrinterW.Call(
		uintptr(unsafe.Pointer(nombreUTF16)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)

	if ret == 0 {
		return fmt.Errorf("no se pudo abrir la impresora \"%s\": %v", nombreImpresora, err)
	}

	defer procClosePrinter.Call(uintptr(handle))

	docName, err := syscall.UTF16PtrFromString("Ticket ComercioCity")
	if err != nil {
		return err
	}

	datatype, err := syscall.UTF16PtrFromString("RAW")
	if err != nil {
		return err
	}

	doc := docInfo1{
		DocName:    docName,
		OutputFile: nil,
		Datatype:   datatype,
	}

	ret, _, err = procStartDocPrinterW.Call(
		uintptr(handle), 1, uintptr(unsafe.Pointer(&doc)),
	)

	if ret == 0 {
		return fmt.Errorf("la impresora \"%s\" no acepto el trabajo: %v", nombreImpresora, err)
	}

	defer procEndDocPrinter.Call(uintptr(handle))

	ret, _, err = procStartPagePrinter.Call(uintptr(handle))
	if ret == 0 {
		return fmt.Errorf("no se pudo empezar a imprimir en \"%s\": %v", nombreImpresora, err)
	}

	defer procEndPagePrinter.Call(uintptr(handle))

	var escritos uint32

	ret, _, err = procWritePrinter.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&contenido[0])),
		uintptr(len(contenido)),
		uintptr(unsafe.Pointer(&escritos)),
	)

	if ret == 0 {
		return fmt.Errorf("fallo al mandar el ticket a \"%s\": %v", nombreImpresora, err)
	}

	if int(escritos) != len(contenido) {
		return fmt.Errorf(
			"el ticket se mando incompleto a \"%s\": %d de %d bytes",
			nombreImpresora, escritos, len(contenido),
		)
	}

	return nil
}

// utf16PunteroAString convierte una cadena terminada en cero que devolvio Windows.
func utf16PunteroAString(puntero *uint16) string {
	if puntero == nil {
		return ""
	}

	var largo int
	for ptr := unsafe.Pointer(puntero); *(*uint16)(ptr) != 0; ptr = unsafe.Pointer(uintptr(ptr) + 2) {
		largo++
		// Cota de seguridad: si por lo que fuera no viniera el cero final, no se recorre memoria
		// ajena para siempre.
		if largo > 4096 {
			break
		}
	}

	if largo == 0 {
		return ""
	}

	return syscall.UTF16ToString(unsafe.Slice(puntero, largo))
}
