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

	// Techo del backoff cuando el servidor no contesta.
	intervaloMaximoConError = 2 * time.Minute

	// Cuanto puede tardar una impresion antes de darla por colgada.
	tiempoMaximoDeImpresion = 45 * time.Second
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
		instalar()
		return
	}

	/*
	 * 🔴 Con config, hay dos casos MUY distintos y confundirlos rompe las actualizaciones:
	 *
	 * - El ejecutable es el instalado: es el arranque automatico de Windows. Se esconde y sondea.
	 * - El ejecutable NO es el instalado: el operador bajo una version nueva y le hizo doble clic
	 *   para actualizar. Si esto se tratara igual que el caso de arriba, el agente veria el mutex
	 *   del que ya esta corriendo, saldria sin hacer nada, y la version nueva NUNCA se instalaria:
	 *   las 40 maquinas se quedarian con la version vieja para siempre.
	 */
	if !EstoyCorriendoDesdeLaInstalacion() {
		actualizar(config)
		return
	}

	OcultarConsola()

	if !TomarMutexDeInstancia() {
		Log("no se arranca: ya hay otra instancia de este usuario corriendo")
		return
	}

	Log("agente iniciado | equipo=%s | servidor=%s", config.NombreEquipo, config.ApiURL)

	sondear(config)
}

// actualizar reemplaza la version instalada por la que se acaba de ejecutar.
func actualizar(config *Config) {
	fmt.Println()
	fmt.Println("  ComercioCity - Impresion directa")
	fmt.Println("  ---------------------------------")
	fmt.Println()
	fmt.Println("  Esta computadora ya esta vinculada como:", config.NombreEquipo)
	fmt.Println("  Actualizando el programa a esta version...")
	fmt.Println()

	/*
	 * El agente viejo tiene tomado su propio .exe, asi que hay que esperar a que salga. Se lo
	 * empuja soltando el mutex: la instancia vieja no lo mira, pero el rename del archivo si falla
	 * mientras siga corriendo. Por eso se avisa explicitamente en vez de fallar en silencio.
	 */
	destino, err := InstalarEnElEquipo()

	if err != nil {
		Log("fallo la actualizacion: %v", err)

		fmt.Println("  No se pudo actualizar el programa:")
		fmt.Println("  ", err)
		fmt.Println()
		fmt.Println("  Suele pasar porque la version anterior esta corriendo.")
		fmt.Println("  Reinicia la computadora y volve a ejecutar este archivo.")
		fmt.Println()
		esperarEnter()
		return
	}

	Log("actualizado desde una ejecucion manual | destino=%s", destino)

	fmt.Println("  LISTO. El programa quedo actualizado.")
	fmt.Println("  Instalado en:", destino)
	fmt.Println()
	fmt.Println("  Se va a seguir ejecutando desde esta ventana.")
	fmt.Println("  Podes minimizarla: no la cierres.")
	fmt.Println()

	esperarEnter()

	OcultarConsola()

	if !TomarMutexDeInstancia() {
		Log("actualizacion: ya hay otra instancia corriendo, no se sondea desde esta")
		return
	}

	sondear(config)
}

// instalar es el primer arranque: vincula el equipo y lo deja andando.
func instalar() {
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
		esperarEnter()
		return
	}

	if len(impresoras) == 0 {
		fmt.Println("  ATENCION: esta computadora no tiene ninguna impresora instalada.")
		fmt.Println("  Instalala primero desde Windows y volve a ejecutar este programa.")
		esperarEnter()
		return
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
		esperarEnter()
		return
	}

	nombre := nombreDelEquipo()

	fmt.Println()
	fmt.Println("  Conectando con el sistema...")

	token, err := vincularEnServidor(codigo.ApiURL, codigo.Codigo, nombre, impresoras)
	if err != nil {
		fmt.Println()
		fmt.Println("  No se pudo vincular:", err)
		esperarEnter()
		return
	}

	config := &Config{
		ApiURL:       codigo.ApiURL,
		Token:        token,
		NombreEquipo: nombre,
	}

	if err := guardarConfig(config); err != nil {
		fmt.Println("  No se pudo guardar la configuracion:", err)
		esperarEnter()
		return
	}

	destino, err := InstalarEnElEquipo()
	if err != nil {
		// La vinculacion ya quedo hecha, asi que esto no es fatal: el agente funciona igual
		// mientras esta abierto. Lo unico que se pierde es el arranque automatico.
		Log("vinculado pero sin arranque automatico: %v", err)

		fmt.Println()
		fmt.Println("  El equipo quedo vinculado, pero no se pudo configurar el arranque automatico:")
		fmt.Println("  ", err)
		fmt.Println("  Vas a tener que abrir este programa a mano cada vez que prendas la computadora.")
	} else {
		Log("instalado | equipo=%s | destino=%s", nombre, destino)
		fmt.Println("  Instalado en:", destino)
	}

	fmt.Println()
	fmt.Println("  LISTO. El equipo quedo conectado como:", nombre)
	fmt.Println()
	fmt.Println("  Volve al sistema: ahi vas a ver esta computadora y sus impresoras")
	fmt.Println("  en la lista, y vas a poder hacer una impresion de prueba.")
	fmt.Println()
	fmt.Println("  Esta ventana se va a esconder sola y el programa va a seguir")
	fmt.Println("  funcionando. De ahora en mas arranca solo cuando prendas la")
	fmt.Println("  computadora, sin que tengas que abrir nada.")
	fmt.Println()

	esperarEnter()

	// Se queda sondeando en esta misma corrida para que el operador pueda probar la impresion
	// desde el sistema sin reiniciar nada.
	OcultarConsola()

	// El mutex se toma tambien aca: si no, esta instancia queda corriendo sin marcar, y el .exe
	// instalado que el operador ejecute despues no la detecta y arrancan dos agentes con el mismo
	// token -- que es como un ticket termina saliendo dos veces.
	if !TomarMutexDeInstancia() {
		return
	}

	sondear(config)
}

// sondear es el bucle de trabajo: pregunta por tickets, los imprime, informa como salieron.
func sondear(config *Config) {
	ultimoTrabajo := time.Now()
	ultimoHeartbeat := time.Time{}
	erroresSeguidos := 0

	for {
		/*
		 * Un panic mata el proceso entero, y con la consola escondida el stack trace va a un
		 * stderr que nadie mira: la caja simplemente deja de imprimir hasta que alguien reinicie
		 * Windows. El recover envuelve UNA vuelta del bucle, asi que un error puntual en una
		 * impresion no se lleva puesto al agente.
		 */
		termina := unaVueltaDelBucle(config, &ultimoTrabajo, &ultimoHeartbeat, &erroresSeguidos)

		if termina {
			return
		}
	}
}

// unaVueltaDelBucle hace un ciclo completo y devuelve true si el agente tiene que terminar.
func unaVueltaDelBucle(config *Config, ultimoTrabajo *time.Time, ultimoHeartbeat *time.Time, erroresSeguidos *int) (termina bool) {
	defer func() {
		if r := recover(); r != nil {
			Log("PANIC recuperado en el bucle: %v", r)
			time.Sleep(5 * time.Second)
		}
	}()

	// El heartbeat va antes que el sondeo para que el equipo aparezca en linea en el sistema
	// desde el primer segundo, sin esperar a que alguien mande un ticket.
	if time.Since(*ultimoHeartbeat) >= intervaloDeHeartbeat {
		impresoras, err := ListarImpresoras()

		if err == nil {
			errHeartbeat := enviarHeartbeat(config, impresoras)

			if errHeartbeat == ErrNoAutorizado {
				desvincularse(config)
				return true
			}

			/*
			 * 🔴 El reloj se adelanta pase lo que pase. Si solo se moviera cuando el POST sale
			 * bien, con el servidor caido la condicion seguiria siendo verdadera en cada vuelta:
			 * el agente pasaria de 1 heartbeat por minuto a 1 cada 2 segundos, golpeando mas
			 * fuerte justo cuando el hosting esta en problemas.
			 */
			*ultimoHeartbeat = time.Now()
		}
	}

	jobs, err := pedirTrabajos(config)

	if err == ErrNoAutorizado {
		desvincularse(config)
		return true
	}

	if err != nil {
		*erroresSeguidos++

		if *erroresSeguidos == 1 || *erroresSeguidos%30 == 0 {
			Log("no se pudo consultar el servidor (%d seguidos): %v", *erroresSeguidos, err)
		}
	} else {
		if *erroresSeguidos > 0 {
			Log("conexion con el servidor restablecida despues de %d errores", *erroresSeguidos)
		}

		*erroresSeguidos = 0

		if len(jobs) > 0 {
			*ultimoTrabajo = time.Now()

			for _, job := range jobs {
				imprimirTrabajo(config, job)
			}
		}
	}

	time.Sleep(esperaHastaLaProximaVuelta(*ultimoTrabajo, *erroresSeguidos))

	return false
}

// esperaHastaLaProximaVuelta decide cuanto dormir.
//
// Con errores seguidos el intervalo crece hasta un techo: sin eso, 40 comercios reintentando cada
// 2 segundos contra un servidor caido son un ataque de denegacion de servicio contra uno mismo.
func esperaHastaLaProximaVuelta(ultimoTrabajo time.Time, erroresSeguidos int) time.Duration {
	if erroresSeguidos > 0 {
		espera := intervaloDeSondeo * time.Duration(1<<uint(minimo(erroresSeguidos, 7)))

		if espera > intervaloMaximoConError {
			return intervaloMaximoConError
		}

		return espera
	}

	if time.Since(ultimoTrabajo) > tiempoHastaReposo {
		return intervaloEnReposo
	}

	return intervaloDeSondeo
}

func minimo(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// desvincularse corta cuando el servidor dice que este equipo ya no existe.
//
// 🔴 Un 401 es terminal: el token no se arregla solo. Sin esto, un equipo desvinculado desde el
// sistema seguiria sondeando para siempre contra un servidor que va a contestar 401 en cada
// intento, sin ventana, sin icono y sin forma de que nadie se entere. Y como el .exe sigue en la
// carpeta de inicio, volveria a arrancar en cada login, para siempre.
func desvincularse(config *Config) {
	Log("el servidor ya no reconoce este equipo (401). Se borra la configuracion y se sale.")

	if err := borrarConfig(); err != nil {
		Log("no se pudo borrar la configuracion: %v", err)
	}

	if err := DesinstalarDelInicio(); err != nil {
		Log("no se pudo sacar el programa del inicio de Windows: %v", err)
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
		Log("trabajo %d: el ticket llego dañado: %v", job.ID, err)
		informarResultado(config, job.ID, "error", "el ticket llego dañado: "+err.Error())
		return
	}

	/*
	 * 🔴 La impresion corre en su propia goroutine con timeout. OpenPrinter y WritePrinter son RPC
	 * contra el spooler: contra una impresora de red inalcanzable, o con el servicio Spooler
	 * colgado, bloquean minutos. Sincronico, eso congelaria el bucle entero -- y con last_seen_at
	 * congelado el sistema empieza a devolverle "el equipo no esta conectado" a TODAS las cajas
	 * que imprimen en este equipo.
	 */
	listo := make(chan error, 1)

	go func() {
		listo <- ImprimirRaw(job.PrinterName, contenido)
	}()

	select {
	case errDeImpresion := <-listo:
		if errDeImpresion != nil {
			Log("trabajo %d: fallo la impresion en %q: %v", job.ID, job.PrinterName, errDeImpresion)
			informarResultado(config, job.ID, "error", errDeImpresion.Error())
			return
		}

		Log("trabajo %d: impreso en %q (%d bytes)", job.ID, job.PrinterName, len(contenido))
		informarResultado(config, job.ID, "impreso", "")

	case <-time.After(tiempoMaximoDeImpresion):
		/*
		 * La goroutine queda colgada dentro del syscall hasta que Windows la suelte. Es una fuga
		 * acotada y conocida, y es preferible a bloquear el bucle: el agente sigue atendiendo al
		 * resto de las cajas mientras esa impresora esta trabada.
		 */
		Log("trabajo %d: la impresora %q no respondio en %s", job.ID, job.PrinterName, tiempoMaximoDeImpresion)
		informarResultado(config, job.ID, "error", "la impresora no respondio: puede estar apagada o desconectada")
	}
}

// esperarEnter deja la ventana abierta para que el operador alcance a leer.
func esperarEnter() {
	fmt.Print("  Apreta Enter para continuar...")
	bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
}
