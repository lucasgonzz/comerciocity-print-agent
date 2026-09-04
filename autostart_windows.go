package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	swHide                = 0
	errorAlreadyExists    = 183
	nombreDelEjecutable   = "ComercioCityPrint.exe"
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

// YaHayOtraInstancia evita que el agente corra dos veces.
//
// Pasa facil: queda el de la carpeta de inicio corriendo y el operador vuelve a hacer doble clic
// en el que bajo a Descargas. Con dos instancias sondeando, cada ticket sale una sola vez igual
// (el servidor lo entrega una vez), pero se duplican los sondeos y el diagnostico se vuelve
// confuso. El mutex es del sistema, asi que lo ve la otra instancia aunque sea otro archivo .exe.
func YaHayOtraInstancia() bool {
	nombre, err := syscall.UTF16PtrFromString(`Global\ComercioCityPrintAgent`)
	if err != nil {
		return false
	}

	ret, _, err := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(nombre)))
	if ret == 0 {
		return false
	}

	if errno, ok := err.(syscall.Errno); ok && uintptr(errno) == errorAlreadyExists {
		return true
	}

	return false
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

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", errNoAppData
	}

	carpetaDestino := filepath.Join(localAppData, "ComercioCityPrint")
	if err := os.MkdirAll(carpetaDestino, 0700); err != nil {
		return "", err
	}

	destino := filepath.Join(carpetaDestino, nombreDelEjecutable)

	// Si ya se esta ejecutando desde el destino, no tiene sentido copiarse sobre si mismo
	// (Windows ademas tiene el archivo tomado y la copia falla).
	if !mismaRuta(origen, destino) {
		if err := copiarArchivo(origen, destino); err != nil {
			return "", fmt.Errorf("no se pudo copiar el programa a %s: %v", carpetaDestino, err)
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

// mismaRuta compara dos rutas sin que las diferencias de mayusculas o de forma cuenten.
func mismaRuta(a, b string) bool {
	rutaA, errA := filepath.Abs(a)
	rutaB, errB := filepath.Abs(b)

	if errA != nil || errB != nil {
		return false
	}

	return len(rutaA) == len(rutaB) && filepath.Clean(rutaA) == filepath.Clean(rutaB)
}

// copiarArchivo copia un archivo entero.
func copiarArchivo(origen, destino string) error {
	entrada, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer entrada.Close()

	salida, err := os.OpenFile(destino, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}
	defer salida.Close()

	if _, err := io.Copy(salida, entrada); err != nil {
		return err
	}

	return salida.Sync()
}
