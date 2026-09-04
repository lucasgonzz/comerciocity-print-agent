package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// El agente corre escondido en la PC de un comercio, sin ventana ni ícono de bandeja. Sin este
// archivo, un agente roto es indistinguible de uno sano desde afuera: el soporte es por teléfono
// y la única pregunta que se puede contestar es "¿está prendida la computadora?".
//
// Se escribe siempre, incluso cuando todo anda, porque el dato que hace falta cuando alguien llama
// es el de hace tres semanas.
var (
	mutexDelLog sync.Mutex

	// Tamaño al que se rota. Un log que crece sin techo en la maquina de un cliente es
	// exactamente el tipo de regalo que uno no quiere dejar instalado.
	tamanoMaximoDelLog = int64(1 << 20)
)

// rutaDelLog devuelve el archivo de log dentro de la carpeta de datos.
func rutaDelLog() (string, error) {
	carpeta, err := carpetaDeDatos()
	if err != nil {
		return "", err
	}

	return filepath.Join(carpeta, "agente.log"), nil
}

// Log escribe una línea con marca de tiempo.
//
// Nunca falla hacia afuera: si no se puede escribir el log, el agente tiene que seguir imprimiendo
// igual. Un problema de diagnóstico no puede convertirse en un problema de operación.
func Log(formato string, args ...interface{}) {
	mutexDelLog.Lock()
	defer mutexDelLog.Unlock()

	linea := time.Now().Format("2006-01-02 15:04:05") + "  " + fmt.Sprintf(formato, args...) + "\r\n"

	ruta, err := rutaDelLog()
	if err != nil {
		return
	}

	rotarSiHaceFalta(ruta)

	archivo, err := os.OpenFile(ruta, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer archivo.Close()

	archivo.WriteString(linea)
}

// rotarSiHaceFalta deja un solo archivo viejo, para que el diagnostico no se pierda entero cuando
// se rota justo despues del problema.
func rotarSiHaceFalta(ruta string) {
	info, err := os.Stat(ruta)
	if err != nil || info.Size() < tamanoMaximoDelLog {
		return
	}

	os.Remove(ruta + ".old")
	os.Rename(ruta, ruta+".old")
}
