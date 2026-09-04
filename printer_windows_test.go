package main

import "testing"

// TestListarImpresoras verifica contra el Windows real que la llamada a EnumPrintersW este bien
// armada: los tamaños de struct y el recorrido del buffer son justo donde un error de syscall no
// da error, devuelve basura.
func TestListarImpresoras(t *testing.T) {
	impresoras, err := ListarImpresoras()
	if err != nil {
		t.Fatalf("ListarImpresoras devolvio error: %v", err)
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
		"vacio":        "",
		"sin prefijo":  "abc123",
		"base64 rota":  "CC1.esto-no-es-base64-valido!!!",
		"json roto":    "CC1.bm8tZXMtanNvbg",
		"sin url":      "CC1.eyJjIjoiYWJjMTIzIn0",
	}

	for nombre, entrada := range casos {
		if _, err := interpretarCodigo(entrada); err == nil {
			t.Errorf("el caso %q tendria que haber fallado y no fallo", nombre)
		}
	}
}
