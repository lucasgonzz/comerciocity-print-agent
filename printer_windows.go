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
	procSetJobW          = winspool.NewProc("SetJobW")
	procGetPrinterW      = winspool.NewProc("GetPrinterW")
)

const (
	// Impresoras instaladas en esta maquina, mas las compartidas a las que esta conectada.
	printerEnumLocal       = 0x00000002
	printerEnumConnections = 0x00000004

	errorInsufficientBuffer = 122

	// Cancela un trabajo ya empezado, para no dejar medio ticket en la comandera.
	jobControlDelete = 5

	// Estados de PRINTER_INFO_6 que valen la pena avisar ANTES de mandar el ticket.
	printerStatusPaused       = 0x00000001
	printerStatusError        = 0x00000002
	printerStatusPaperJam     = 0x00000008
	printerStatusPaperOut     = 0x00000010
	printerStatusOffline      = 0x00000080
	printerStatusDoorOpen     = 0x00400000
	printerStatusNotAvailable = 0x00001000
)

// printerInfo4 es PRINTER_INFO_4W. Es el nivel mas barato de EnumPrinters: no consulta al driver
// ni al puerto, solo devuelve el nombre. Con niveles mas altos, una impresora de red apagada puede
// hacer que la enumeracion tarde varios segundos.
type printerInfo4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

// printerInfo6 es PRINTER_INFO_6: solo el estado. Es la estructura mas barata para preguntar si la
// impresora esta en condiciones de recibir el trabajo.
type printerInfo6 struct {
	Status uint32
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
//
// Reintenta porque hay una carrera inevitable: entre la llamada que pregunta cuanto buffer hace
// falta y la que trae los datos, puede aparecer una impresora (se prende una de red, alguien
// instala una) y la segunda vuelve pidiendo mas lugar. Sin reintento eso aborta la instalacion
// entera con un error que no le dice nada a nadie.
func ListarImpresoras() ([]string, error) {
	var ultimoError error

	for intento := 0; intento < 3; intento++ {
		impresoras, err := listarImpresorasUnaVez()
		if err == nil {
			return impresoras, nil
		}

		ultimoError = err
	}

	return nil, ultimoError
}

func listarImpresorasUnaVez() ([]string, error) {
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
		desplazamiento := uintptr(i) * unsafe.Sizeof(printerInfo4{})

		// Guarda contra un devueltos que no cierre con el buffer: leer fuera seria memoria ajena.
		if desplazamiento+unsafe.Sizeof(printerInfo4{}) > uintptr(len(buffer)) {
			break
		}

		info := (*printerInfo4)(unsafe.Pointer(&buffer[desplazamiento]))

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

// EstadoDeImpresora devuelve un motivo si la impresora no esta en condiciones de imprimir.
//
// 🔴 Existe porque el spooler ACEPTA trabajos para una impresora apagada, sin papel o
// desenchufada: los encola y espera. Sin este chequeo, el agente informaria "impreso" y el
// operador estaria mirando una comandera que no saca nada. Es el motivo numero uno de llamado al
// soporte, y el sistema estaria diseñado para mentir sobre el.
//
// Devuelve "" si la impresora esta lista.
func EstadoDeImpresora(nombreImpresora string) string {
	nombreUTF16, err := syscall.UTF16PtrFromString(nombreImpresora)
	if err != nil {
		return ""
	}

	var handle syscall.Handle

	ret, _, _ := procOpenPrinterW.Call(
		uintptr(unsafe.Pointer(nombreUTF16)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)

	if ret == 0 {
		return ""
	}

	defer procClosePrinter.Call(uintptr(handle))

	var necesarios uint32

	// Nivel 6: solo el estado.
	procGetPrinterW.Call(uintptr(handle), 6, 0, 0, uintptr(unsafe.Pointer(&necesarios)))

	if necesarios == 0 {
		return ""
	}

	buffer := make([]byte, necesarios)

	ret, _, _ = procGetPrinterW.Call(
		uintptr(handle), 6,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(necesarios),
		uintptr(unsafe.Pointer(&necesarios)),
	)

	if ret == 0 || uintptr(len(buffer)) < unsafe.Sizeof(printerInfo6{}) {
		return ""
	}

	estado := (*printerInfo6)(unsafe.Pointer(&buffer[0])).Status

	// El orden importa: se informa el motivo mas accionable para el operador.
	if estado&printerStatusPaperOut != 0 {
		return "la impresora no tiene papel"
	}
	if estado&printerStatusPaperJam != 0 {
		return "la impresora tiene el papel trabado"
	}
	if estado&printerStatusDoorOpen != 0 {
		return "la impresora tiene la tapa abierta"
	}
	if estado&printerStatusOffline != 0 {
		return "la impresora esta apagada o desconectada"
	}
	if estado&printerStatusNotAvailable != 0 {
		return "la impresora no esta disponible"
	}
	if estado&printerStatusPaused != 0 {
		return "la impresora esta en pausa en Windows"
	}
	if estado&printerStatusError != 0 {
		return "la impresora esta en estado de error"
	}

	return ""
}

// ImprimirRaw manda los bytes tal cual a la impresora indicada.
//
// `contenido` son bytes ESC/POS ya armados por el sistema: comandos de negrita, corte de papel y
// texto. Este agente no los interpreta ni los modifica.
func ImprimirRaw(nombreImpresora string, contenido []byte) error {
	if len(contenido) == 0 {
		return errors.New("el ticket llego vacio")
	}

	if motivo := EstadoDeImpresora(nombreImpresora); motivo != "" {
		return errors.New(motivo)
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

	// StartDocPrinter devuelve el id del trabajo, que es lo que despues permite cancelarlo.
	jobID, _, err := procStartDocPrinterW.Call(
		uintptr(handle), 1, uintptr(unsafe.Pointer(&doc)),
	)

	if jobID == 0 {
		return fmt.Errorf("la impresora \"%s\" no acepto el trabajo: %v", nombreImpresora, err)
	}

	/*
	 * 🔴 El error de escritura se guarda y se maneja al final, en vez de retornar en el momento.
	 *
	 * Si se retornara de una, los defer de EndPagePrinter/EndDocPrinter correrian igual y eso le
	 * dice al spooler "dale, imprimí": saldria medio ticket, sin el comando de corte -- que va al
	 * final del payload --, con el papel sin cortar y a mitad de una secuencia de escape. El
	 * operador se queda con un papel a medias y un cartel que dice que no imprimio.
	 *
	 * Con el trabajo cancelado por SetJob, el spooler lo descarta entero y no sale nada.
	 */
	var errorDeEscritura error

	ret, _, err = procStartPagePrinter.Call(uintptr(handle))
	if ret == 0 {
		errorDeEscritura = fmt.Errorf("no se pudo empezar a imprimir en \"%s\": %v", nombreImpresora, err)
	} else {
		errorDeEscritura = escribirTodo(handle, nombreImpresora, contenido)
		procEndPagePrinter.Call(uintptr(handle))
	}

	procEndDocPrinter.Call(uintptr(handle))

	if errorDeEscritura != nil {
		procSetJobW.Call(uintptr(handle), jobID, 0, 0, jobControlDelete)
		return errorDeEscritura
	}

	return nil
}

// escribirTodo manda el contenido completo, en varias pasadas si hace falta.
//
// WritePrinter puede escribir MENOS de lo que se le pidio sin que eso sea un error: el contrato es
// que el llamador siga desde donde quedo. Tratarlo como error definitivo hace fallar los tickets
// largos (una comanda de muchos renglones, un cierre de caja) justo cuando el spooler esta cargado.
func escribirTodo(handle syscall.Handle, nombreImpresora string, contenido []byte) error {
	restante := contenido

	for len(restante) > 0 {
		var escritos uint32

		ret, _, err := procWritePrinter.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&restante[0])),
			uintptr(len(restante)),
			uintptr(unsafe.Pointer(&escritos)),
		)

		if ret == 0 {
			return fmt.Errorf("fallo al mandar el ticket a \"%s\": %v", nombreImpresora, err)
		}

		if escritos == 0 {
			return fmt.Errorf("la impresora \"%s\" dejo de aceptar datos a mitad del ticket", nombreImpresora)
		}

		restante = restante[escritos:]
	}

	return nil
}

// utf16PunteroAString convierte una cadena terminada en cero que devolvio Windows.
func utf16PunteroAString(puntero *uint16) string {
	if puntero == nil {
		return ""
	}

	// Cota dura: si por lo que fuera no viniera el cero final, se corta ANTES de seguir
	// dereferenciando. El limite se chequea antes de avanzar, no despues.
	const maximo = 1024

	var largo int

	for largo < maximo {
		actual := (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(puntero)) + uintptr(largo)*2))
		if *actual == 0 {
			break
		}
		largo++
	}

	if largo == 0 {
		return ""
	}

	return syscall.UTF16ToString(unsafe.Slice(puntero, largo))
}
