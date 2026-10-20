package main

import "testing"

// TestMensajeDeError cubre lo que el operador lee cuando falla un pedido al servidor: el motivo que
// mando el sistema si lo mando, y un texto util cuando quien contesto no fue el sistema.
func TestMensajeDeError(t *testing.T) {
	casos := []struct {
		nombre   string
		cuerpo   string
		status   int
		esperado string
	}{
		{
			"el sistema explica el motivo en error",
			`{"error":"El codigo no es valido o ya se uso"}`,
			404,
			"El codigo no es valido o ya se uso",
		},
		{
			"el sistema explica el motivo en message",
			`{"message":"Too Many Attempts."}`,
			429,
			"Too Many Attempts.",
		},
		{
			"404 en HTML: lo contesto el hosting y no el sistema",
			`<!DOCTYPE html><html><head><title>404 Not Found</title></head><body>404</body></html>`,
			404,
			"no se encontro el servicio de impresion en esa direccion, avisa a soporte",
		},
		{
			"404 sin cuerpo",
			``,
			404,
			"no se encontro el servicio de impresion en esa direccion, avisa a soporte",
		},
		{
			"otro status sin JSON conserva el mensaje de siempre",
			`<html>Bad Gateway</html>`,
			502,
			"el servidor respondio 502",
		},
		{
			"JSON sin motivo conserva el mensaje de siempre",
			`{}`,
			404,
			"el servidor respondio 404",
		},
	}

	for _, caso := range casos {
		err := mensajeDeError([]byte(caso.cuerpo), caso.status)
		if err == nil || err.Error() != caso.esperado {
			t.Errorf("%s: se esperaba %q y se obtuvo %v", caso.nombre, caso.esperado, err)
		}
	}
}

// TestMensajeDeErrorNoAutorizado confirma que el 401 sigue siendo el error terminal de siempre,
// con cualquier cuerpo.
func TestMensajeDeErrorNoAutorizado(t *testing.T) {
	if err := mensajeDeError([]byte(`<html>nope</html>`), 401); err != ErrNoAutorizado {
		t.Errorf("el 401 tendria que ser ErrNoAutorizado y fue %v", err)
	}
}
