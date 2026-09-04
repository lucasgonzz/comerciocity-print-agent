// Agente de impresion de ComercioCity.
//
// Corre en la computadora de la caja y es el puente entre el sistema y la comandera termica.
// Reemplaza a QZ Tray, con una diferencia de fondo: en vez de escuchar en un puerto local para que
// el navegador le hable, sale hacia el servidor y pregunta si hay algo para imprimir.
//
// Eso resuelve de una sola vez cuatro cosas que con QZ no tenian arreglo: el cartel de permiso en
// cada impresion, el prompt de Local Network Access que Chrome 141 introdujo sobre localhost, el
// mixed content, y tener que abrir puertos en la red del comercio. Y suma una: se puede mandar a
// imprimir desde otro dispositivo, porque quien imprime ya no necesita estar en la misma maquina
// que la impresora.
package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// Cada cuanto pregunta si hay tickets. Dos segundos es el compromiso entre que el ticket salga
	// enseguida y no golpear el hosting compartido: un long-poll seria mas inmediato pero deja
	// tomado un worker de PHP durante toda la espera, y con varias cajas por comercio eso lo voltea.
	intervaloDeSondeo = 2 * time.Second

	// En reposo el sondeo se espacia. Una caja cerrada de noche no necesita preguntar cada 2s.
	intervaloEnReposo = 8 * time.Second

	// Despues de este tiempo sin ningun ticket, se pasa al intervalo de reposo.
	tiempoHastaReposo = 3 * time.Minute

	// Cada cuanto reporta las impresoras del equipo aunque no haya trabajo.
	intervaloDeHeartbeat = 60 * time.Second
)

func main() {
	config, err := leerConfig()
	if err != nil {
		fmt.Println("No se pudo leer la configuracion:", err)
		esperarEnter()
		os.Exit(1)
	}

	// Sin configuracion, este es el primer arranque: hay alguien mirando la pantalla.
	if config == nil {
		if !instalar() {
			esperarEnter()
			os.Exit(1)
		}
		return
	}

	// Con configuracion, arranco solo con Windows: nadie esta mirando y la ventana molesta.
	OcultarConsola()

	if YaHayOtraInstancia() {
		return
	}

	sondear(config)
}

// instalar es el primer arranque: vincula el equipo y lo deja andando.
func instalar() bool {
	fmt.Println()
	fmt.Println("  ComercioCity - Impresion directa")
	fmt.Println("  ---------------------------------")
	fmt.Println()
	fmt.Println("  Este programa conecta tu comandera con el sistema.")
	fmt.Println("  Se instala una sola vez y despues arranca solo.")
	fmt.Println()

	impresoras, err := ListarImpresoras()
	if err != nil {
		fmt.Println("  No se pudieron leer las impresoras de esta computadora:", err)
		return false
	}

	if len(impresoras) == 0 {
		fmt.Println("  ATENCION: esta computadora no tiene ninguna impresora instalada.")
		fmt.Println("  Instalala primero desde Windows y volve a ejecutar este programa.")
		return false
	}

	fmt.Printf("  Impresoras encontradas (%d):\n", len(impresoras))
	for _, impresora := range impresoras {
		fmt.Println("    -", impresora)
	}
	fmt.Println()

	fmt.Println("  Pega aca el codigo que te dio el sistema y apreta Enter.")
	fmt.Println("  (lo copiaste con el boton \"Copiar codigo\" en la pantalla de impresion)")
	fmt.Println()
	fmt.Print("  Codigo: ")

	lector := bufio.NewReader(os.Stdin)
	pegado, _ := lector.ReadString('\n')

	codigo, err := interpretarCodigo(pegado)
	if err != nil {
		fmt.Println()
		fmt.Println("  " + err.Error())
		return false
	}

	nombre := nombreDelEquipo()

	fmt.Println()
	fmt.Println("  Conectando con el sistema...")

	token, err := vincularEnServidor(codigo.ApiURL, codigo.Codigo, nombre, impresoras)
	if err != nil {
		fmt.Println()
		fmt.Println("  No se pudo vincular:", err)
		return false
	}

	config := &Config{
		ApiURL:       codigo.ApiURL,
		Token:        token,
		NombreEquipo: nombre,
	}

	if err := guardarConfig(config); err != nil {
		fmt.Println("  No se pudo guardar la configuracion:", err)
		return false
	}

	destino, err := InstalarEnElEquipo()
	if err != nil {
		// La vinculacion ya quedo hecha, asi que esto no es fatal: el agente funciona igual
		// mientras esta abierto. Lo unico que se pierde es el arranque automatico.
		fmt.Println()
		fmt.Println("  El equipo quedo vinculado, pero no se pudo configurar el arranque automatico:")
		fmt.Println("  ", err)
		fmt.Println("  Vas a tener que abrir este programa a mano cada vez que prendas la computadora.")
	} else {
		fmt.Println("  Instalado en:", destino)
	}

	fmt.Println()
	fmt.Println("  LISTO. El equipo quedo conectado como:", nombre)
	fmt.Println()
	fmt.Println("  Ya podes cerrar esta ventana y volver al sistema:")
	fmt.Println("  ahi vas a ver esta computadora y sus impresoras en la lista.")
	fmt.Println()
	fmt.Println("  De ahora en mas arranca solo cuando prendas la computadora.")
	fmt.Println("  Si queres, ya podes borrar el archivo que descargaste.")
	fmt.Println()

	esperarEnter()

	// Se queda sondeando en esta misma corrida para que el operador pueda probar la impresion
	// desde el sistema sin reiniciar nada.
	OcultarConsola()
	sondear(config)

	return true
}

// sondear es el bucle de trabajo: pregunta por tickets, los imprime, informa como salieron.
func sondear(config *Config) {
	ultimoTrabajo := time.Now()
	ultimoHeartbeat := time.Time{}

	for {
		// El heartbeat va antes que el sondeo para que el equipo aparezca en linea en el sistema
		// desde el primer segundo, sin esperar a que alguien mande un ticket.
		if time.Since(ultimoHeartbeat) >= intervaloDeHeartbeat {
			impresoras, err := ListarImpresoras()
			if err == nil {
				if enviarHeartbeat(config, impresoras) == nil {
					ultimoHeartbeat = time.Now()
				}
			}
		}

		jobs, err := pedirTrabajos(config)

		if err == nil && len(jobs) > 0 {
			ultimoTrabajo = time.Now()

			for _, job := range jobs {
				imprimirTrabajo(config, job)
			}
		}

		intervalo := intervaloDeSondeo
		if time.Since(ultimoTrabajo) > tiempoHastaReposo {
			intervalo = intervaloEnReposo
		}

		time.Sleep(intervalo)
	}
}

// imprimirTrabajo manda un ticket a la comandera e informa el resultado.
//
// Nunca corta el bucle: si un ticket falla -- impresora apagada, sin papel, nombre que ya no
// existe --, se informa y se sigue con el siguiente. Una comandera apagada no puede dejar al
// equipo entero sin imprimir.
func imprimirTrabajo(config *Config, job Job) {
	contenido, err := base64.StdEncoding.DecodeString(job.PayloadBase64)
	if err != nil {
		informarResultado(config, job.ID, "error", "el ticket llego danado: "+err.Error())
		return
	}

	if err := ImprimirRaw(job.PrinterName, contenido); err != nil {
		informarResultado(config, job.ID, "error", err.Error())
		return
	}

	informarResultado(config, job.ID, "impreso", "")
}

// esperarEnter deja la ventana abierta para que el operador alcance a leer.
func esperarEnter() {
	fmt.Print("  Apreta Enter para cerrar...")
	bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println(strings.Repeat(" ", 0))
}
