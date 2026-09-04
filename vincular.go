package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// codigoPegado es lo que el sistema le da al operador para copiar.
//
// Lleva adentro la direccion de la API ademas del codigo, para que el cliente no tenga que tipear
// ninguna direccion de internet: copia de un boton, pega, y el agente ya sabe con quien hablar.
type codigoPegado struct {
	ApiURL string `json:"u"`
	Codigo string `json:"c"`
}

// prefijoDelCodigo identifica los codigos de ComercioCity. Existe para poder decir "esto no es un
// codigo del sistema" cuando alguien pega cualquier otra cosa, en vez de fallar con un error de
// formato que no le dice nada a nadie.
const prefijoDelCodigo = "CC1."

// interpretarCodigo valida y abre el codigo que pego el operador.
func interpretarCodigo(pegado string) (*codigoPegado, error) {
	pegado = strings.TrimSpace(pegado)

	// Se sacan comillas por si el operador copio de mas: pasa seguido y es una falla boba.
	pegado = strings.Trim(pegado, "\"'")

	if pegado == "" {
		return nil, errors.New("no pegaste nada")
	}

	if !strings.HasPrefix(pegado, prefijoDelCodigo) {
		return nil, errors.New("eso no parece un codigo de ComercioCity. Copialo de nuevo desde el sistema, con el boton Copiar codigo")
	}

	crudo := strings.TrimPrefix(pegado, prefijoDelCodigo)

	datos, err := base64.RawURLEncoding.DecodeString(crudo)
	if err != nil {
		return nil, errors.New("el codigo esta incompleto o cortado. Copialo de nuevo, entero")
	}

	var codigo codigoPegado
	if err := json.Unmarshal(datos, &codigo); err != nil {
		return nil, errors.New("el codigo esta danado. Generá uno nuevo desde el sistema")
	}

	if codigo.ApiURL == "" || codigo.Codigo == "" {
		return nil, errors.New("el codigo esta incompleto. Generá uno nuevo desde el sistema")
	}

	return &codigo, nil
}

// nombreDelEquipo es como va a aparecer esta computadora en el sistema.
func nombreDelEquipo() string {
	if nombre := os.Getenv("COMPUTERNAME"); nombre != "" {
		return nombre
	}

	if nombre, err := os.Hostname(); err == nil && nombre != "" {
		return nombre
	}

	return "Equipo sin nombre"
}
