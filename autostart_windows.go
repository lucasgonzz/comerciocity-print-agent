package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
	procCreateMutexW     = kernel32.NewProc("CreateMutexW")
)

const (
	swHide              = 0
	errorAlreadyExists  = 183
	nombreDelEjecutable = "ComercioCityPrint.exe"
)

// OcultarConsola esconde la ventana negra cuando el agente arranca solo con Windows.
//
// El ejecutable se compila como aplicacion de consola porque la necesita durante la instalacion
// (ahi el operador pega el codigo y lee los mensajes). En el arranque automatico esa ventana no
// tiene nada que hacer: molesta en la pantalla de la caja y alguien la termina cerrando, que es
// como se apaga el agente sin querer.
func OcultarConsola() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swHide)
	}
}

// TomarMutexDeInstancia evita que el agente corra dos veces a la vez.
//
// 🔴 El alcance es `Local\` y no `Global\`, a proposito: la config vive en %APPDATA% y el arranque
// automatico en la carpeta de inicio del usuario, o sea que TODO en este agente es por usuario de
// Windows. Con `Global\`, en una caja con dos cuentas (turno mañana y turno tarde) el agente del
// segundo usuario veria el mutex del primero, saldria sin decir nada, y esa sesion no imprimiria
// nunca sin ningun mensaje que lo explique.
//
// Devuelve false si ya hay otra instancia de este usuario corriendo.
func TomarMutexDeInstancia() bool {
	nombre, err := syscall.UTF16PtrFromString(`Local\ComercioCityPrintAgent`)
	if err != nil {
		return true
	}

	ret, _, err := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(nombre)))
	if ret == 0 {
		return true
	}

	if errno, ok := err.(syscall.Errno); ok && uintptr(errno) == errorAlreadyExists {
		return false
	}

	return true
}

// carpetaDeInicio es la carpeta Startup del usuario.
//
// 🔴 Se usa esto y no el registro ni un servicio de Windows: no pide permisos de administrador
// (clave, porque en la mayoria de los comercios la sesion de la caja no es admin), no necesita COM
// para armar un acceso directo, y se desinstala borrando un archivo. Windows ejecuta cualquier .exe
// que encuentre ahi al iniciar sesion.
func carpetaDeInicio() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errNoAppData
	}

	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup"), nil
}

// RutaInstalada es donde vive el agente una vez instalado.
func RutaInstalada() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", errNoAppData
	}

	return filepath.Join(localAppData, "ComercioCityPrint", nombreDelEjecutable), nil
}

// EstoyCorriendoDesdeLaInstalacion dice si este proceso ES el agente instalado, y no una copia que
// el operador ejecuto desde Descargas.
func EstoyCorriendoDesdeLaInstalacion() bool {
	origen, err := os.Executable()
	if err != nil {
		return false
	}

	destino, err := RutaInstalada()
	if err != nil {
		return false
	}

	return mismaRuta(origen, destino)
}

// InstalarEnElEquipo deja el agente corriendo solo cada vez que se prende la computadora.
//
// Se copia a dos lados: a una carpeta propia bajo el perfil del usuario (que es de donde va a
// correr siempre) y a la carpeta de inicio. Asi el operador puede borrar tranquilo el .exe que le
// quedo en Descargas, que es lo primero que hace cualquiera.
func InstalarEnElEquipo() (string, error) {
	origen, err := os.Executable()
	if err != nil {
		return "", err
	}

	destino, err := RutaInstalada()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(destino), 0700); err != nil {
		return "", err
	}

	// Si ya se esta ejecutando desde el destino, no tiene sentido copiarse sobre si mismo
	// (Windows ademas tiene el archivo tomado y la copia falla).
	if !mismaRuta(origen, destino) {
		if err := copiarArchivo(origen, destino); err != nil {
			return "", fmt.Errorf("no se pudo copiar el programa a %s: %v", filepath.Dir(destino), err)
		}
	}

	inicio, err := carpetaDeInicio()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(inicio, 0700); err != nil {
		return "", err
	}

	enInicio := filepath.Join(inicio, nombreDelEjecutable)

	if !mismaRuta(origen, enInicio) {
		if err := copiarArchivo(destino, enInicio); err != nil {
			return "", fmt.Errorf("no se pudo dejar el programa en el inicio de Windows: %v", err)
		}
	}

	return destino, nil
}

// mismaRuta compara dos rutas sin que las diferencias de mayusculas cuenten.
//
// En Windows `C:\Users\Caja\...` y `c:\users\caja\...` son el mismo archivo. Compararlas con `==`
// da distinto y hace que el agente intente copiarse sobre si mismo, lo que Windows rechaza con
// sharing violation: la instalacion falla por una razon inventada.
func mismaRuta(a, b string) bool {
	rutaA, errA := filepath.Abs(a)
	rutaB, errB := filepath.Abs(b)

	if errA != nil || errB != nil {
		return false
	}

	return strings.EqualFold(filepath.Clean(rutaA), filepath.Clean(rutaB))
}

// copiarArchivo copia un archivo entero, de forma atomica.
//
// 🔴 Escribe a un temporal y despues renombra, en vez de truncar el destino y copiar encima. Si la
// copia muere a mitad -- disco lleno, un antivirus interceptando la escritura de un .exe en la
// carpeta de inicio, que es JUSTO el patron que Defender vigila --, truncar dejaria un ejecutable
// corrupto en el arranque de Windows, que en el proximo login tira "no es una aplicacion Win32
// valida". Con temporal + rename, o queda el archivo entero o no queda nada.
func copiarArchivo(origen, destino string) error {
	entrada, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer entrada.Close()

	temporal := destino + ".tmp"

	salida, err := os.OpenFile(temporal, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}

	if _, err := io.Copy(salida, entrada); err != nil {
		salida.Close()
		os.Remove(temporal)
		return err
	}

	if err := salida.Sync(); err != nil {
		salida.Close()
		os.Remove(temporal)
		return err
	}

	if err := salida.Close(); err != nil {
		os.Remove(temporal)
		return err
	}

	// En Windows, Rename sobre un archivo existente falla: hay que sacarlo primero. Si el destino
	// esta tomado por un proceso corriendo, esto falla y el temporal se limpia.
	os.Remove(destino)

	if err := os.Rename(temporal, destino); err != nil {
		os.Remove(temporal)
		return err
	}

	return nil
}

// DesinstalarDelInicio saca el agente del arranque de Windows.
//
// Va de la mano de borrarConfig: si el equipo fue desvinculado desde el sistema, dejar el .exe en
// la carpeta de inicio lo haria arrancar en cada login para siempre, sin ventana y sin nada que
// hacer, salvo pedir un codigo nuevo.
func DesinstalarDelInicio() error {
	inicio, err := carpetaDeInicio()
	if err != nil {
		return err
	}

	enInicio := filepath.Join(inicio, nombreDelEjecutable)

	if err := os.Remove(enInicio); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
