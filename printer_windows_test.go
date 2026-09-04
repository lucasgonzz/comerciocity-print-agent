package main

import (
	"testing"
	"time"
)

// TestListarImpresoras verifica contra el Windows real que la llamada a EnumPrintersW este bien
// armada: los tamaños de struct y el recorrido del buffer son justo donde un error de syscall no
// da error, devuelve basura.
func TestListarImpresoras(t *testing.T) {
	impresoras, err := ListarImpresoras()
	if err != nil {
		t.Fatalf("ListarImpresoras devolvio error: %v", err)
	}

	// Sin impresoras el test no verifica nada: se saltea explicitamente en vez de pasar en
	// vacio y dar un verde que no significa nada.
	if len(impresoras) == 0 {
		t.Skip("esta maquina no tiene impresoras instaladas")
	}

	t.Logf("impresoras encontradas: %d", len(impresoras))

	for _, impresora := range impresoras {
		t.Logf("  - %q", impresora)

		if impresora == "" {
			t.Error("se colo un nombre vacio en la lista")
		}

		// Un recorrido mal hecho del buffer devuelve nombres con caracteres nulos o basura
		// binaria en vez de fallar, asi que se revisa el contenido y no solo que haya algo.
		for _, r := range impresora {
			if r == 0 {
				t.Errorf("el nombre %q trae un caracter nulo: el buffer se esta leyendo mal", impresora)
			}
		}
	}
}

// TestInterpretarCodigo cubre el formato del codigo que pega el operador.
func TestInterpretarCodigo(t *testing.T) {
	// Generado con el mismo formato que arma PrintAgentController@codigo.
	valido := "CC1.eyJ1IjoiaHR0cHM6Ly9hcGktZGVtby5jb21lcmNpb2NpdHkuY29tIiwiYyI6ImFiYzEyMyJ9"

	codigo, err := interpretarCodigo(valido)
	if err != nil {
		t.Fatalf("un codigo valido fue rechazado: %v", err)
	}

	if codigo.ApiURL != "https://api-demo.comerciocity.com" {
		t.Errorf("api url mal interpretada: %q", codigo.ApiURL)
	}

	if codigo.Codigo != "abc123" {
		t.Errorf("codigo mal interpretado: %q", codigo.Codigo)
	}

	// Con espacios y comillas alrededor, que es como queda cuando alguien copia de mas.
	if _, err := interpretarCodigo("  \"" + valido + "\"  \r\n"); err != nil {
		t.Errorf("no tolero espacios y comillas alrededor: %v", err)
	}

	casos := map[string]string{
		"vacio":       "",
		"sin prefijo": "abc123",
		"base64 rota": "CC1.esto-no-es-base64-valido!!!",
		"json roto":   "CC1.bm8tZXMtanNvbg",
		"sin url":     "CC1.eyJjIjoiYWJjMTIzIn0",
	}

	for nombre, entrada := range casos {
		if _, err := interpretarCodigo(entrada); err == nil {
			t.Errorf("el caso %q tendria que haber fallado y no fallo", nombre)
		}
	}
}

// TestValidarApiURL cubre la guarda de seguridad del codigo de vinculacion: sin ella, un codigo
// armado a mano apunta el agente a cualquier host y le hace imprimir lo que ese host devuelva.
func TestValidarApiURL(t *testing.T) {
	if err := ValidarApiURL("https://api-demo.comerciocity.com"); err != nil {
		t.Errorf("una url valida fue rechazada: %v", err)
	}

	rechazables := map[string]string{
		"http sin cifrar": "http://api-demo.comerciocity.com",
		"sin host":        "https://",
		"vacia":           "",
		"otro esquema":    "file:///c:/algo",
		"no es una url":   "no-es-una-url",
	}

	for nombre, entrada := range rechazables {
		if err := ValidarApiURL(entrada); err == nil {
			t.Errorf("el caso %q tendria que haber sido rechazado", nombre)
		}
	}
}

// TestInterpretarCodigoRechazaHttp confirma que la validacion de URL esta cableada al camino real
// que usa el operador cuando pega un codigo.
func TestInterpretarCodigoRechazaHttp(t *testing.T) {
	// {"u":"http://malicioso.example","c":"abc123"}
	malicioso := "CC1.eyJ1IjoiaHR0cDovL21hbGljaW9zby5leGFtcGxlIiwiYyI6ImFiYzEyMyJ9"

	if _, err := interpretarCodigo(malicioso); err == nil {
		t.Error("un codigo con http:// tendria que ser rechazado y no lo fue")
	}
}

// TestEsperaHastaLaProximaVuelta cubre el backoff: sin el, con el servidor caido el agente pasa a
// golpear cada 2 segundos justo cuando el hosting esta en problemas.
func TestEsperaHastaLaProximaVuelta(t *testing.T) {
	recien := time.Now()

	if espera := esperaHastaLaProximaVuelta(recien, 0); espera != intervaloDeSondeo {
		t.Errorf("sin errores y con trabajo reciente tendria que sondear cada %s, y da %s", intervaloDeSondeo, espera)
	}

	viejo := time.Now().Add(-10 * time.Minute)
	if espera := esperaHastaLaProximaVuelta(viejo, 0); espera != intervaloEnReposo {
		t.Errorf("en reposo tendria que esperar %s, y da %s", intervaloEnReposo, espera)
	}

	// Con errores, la espera tiene que CRECER y no quedarse en el intervalo normal.
	unError := esperaHastaLaProximaVuelta(recien, 1)
	variosErrores := esperaHastaLaProximaVuelta(recien, 5)

	if unError <= intervaloDeSondeo {
		t.Errorf("con un error la espera tendria que crecer, y da %s", unError)
	}

	if variosErrores <= unError {
		t.Errorf("con mas errores la espera tendria que crecer mas: 1 error da %s y 5 dan %s", unError, variosErrores)
	}

	if techo := esperaHastaLaProximaVuelta(recien, 50); techo > intervaloMaximoConError {
		t.Errorf("la espera supero el techo de %s: %s", intervaloMaximoConError, techo)
	}
}
