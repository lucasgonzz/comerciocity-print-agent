package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var errNoAppData = errors.New("no se pudo ubicar la carpeta de datos del usuario (APPDATA)")

// clienteHTTP tiene timeout a proposito: sin el, un servidor que acepta la conexion y no contesta
// deja al agente colgado para siempre y la caja deja de imprimir sin que nadie sepa por que.
var clienteHTTP = &http.Client{Timeout: 20 * time.Second}

// Job es un ticket esperando salir por la comandera.
type Job struct {
	ID            int    `json:"id"`
	PrinterName   string `json:"printer_name"`
	PayloadBase64 string `json:"payload_base64"`
}

// pedir arma y ejecuta un request contra la API del comercio.
func pedir(metodo, apiURL, ruta, token string, cuerpo interface{}) ([]byte, int, error) {
	var lector io.Reader

	if cuerpo != nil {
		serializado, err := json.Marshal(cuerpo)
		if err != nil {
			return nil, 0, err
		}
		lector = bytes.NewReader(serializado)
	}

	url := strings.TrimRight(apiURL, "/") + "/api/" + strings.TrimLeft(ruta, "/")

	req, err := http.NewRequest(metodo, url, lector)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Accept", "application/json")
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Print-Agent-Token", token)
	}

	res, err := clienteHTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()

	respuesta, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, res.StatusCode, err
	}

	return respuesta, res.StatusCode, nil
}

// mensajeDeError saca el texto que el servidor mando en `error`, para poder mostrarle al operador
// el motivo real (codigo vencido, codigo ya usado) en vez de un numero de status.
func mensajeDeError(cuerpo []byte, status int) error {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}

	if json.Unmarshal(cuerpo, &payload) == nil {
		if payload.Error != "" {
			return errors.New(payload.Error)
		}
		if payload.Message != "" {
			return errors.New(payload.Message)
		}
	}

	return fmt.Errorf("el servidor respondio %d", status)
}

// vincularEnServidor canjea el codigo por el token permanente del equipo.
func vincularEnServidor(apiURL, codigo, nombreEquipo string, impresoras []string) (string, error) {
	cuerpo := map[string]interface{}{
		"codigo":        codigo,
		"nombre_equipo": nombreEquipo,
		"impresoras":    impresoras,
	}

	respuesta, status, err := pedir("POST", apiURL, "print-agent/vincular", "", cuerpo)
	if err != nil {
		return "", err
	}

	if status != 200 {
		return "", mensajeDeError(respuesta, status)
	}

	var payload struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(respuesta, &payload); err != nil {
		return "", err
	}

	if payload.Token == "" {
		return "", errors.New("el servidor no devolvio el token del equipo")
	}

	return payload.Token, nil
}

// enviarHeartbeat avisa que el equipo sigue vivo y actualiza la lista de impresoras.
func enviarHeartbeat(config *Config, impresoras []string) error {
	cuerpo := map[string]interface{}{"impresoras": impresoras}

	respuesta, status, err := pedir("POST", config.ApiURL, "print-agent/heartbeat", config.Token, cuerpo)
	if err != nil {
		return err
	}

	if status != 200 {
		return mensajeDeError(respuesta, status)
	}

	return nil
}

// pedirTrabajos trae los tickets pendientes de este equipo.
func pedirTrabajos(config *Config) ([]Job, error) {
	respuesta, status, err := pedir("GET", config.ApiURL, "print-agent/jobs", config.Token, nil)
	if err != nil {
		return nil, err
	}

	if status != 200 {
		return nil, mensajeDeError(respuesta, status)
	}

	var payload struct {
		Jobs []Job `json:"jobs"`
	}

	if err := json.Unmarshal(respuesta, &payload); err != nil {
		return nil, err
	}

	return payload.Jobs, nil
}

// informarResultado le dice al servidor como salio un trabajo.
func informarResultado(config *Config, jobID int, status string, errorDeImpresion string) error {
	cuerpo := map[string]interface{}{"status": status}

	if errorDeImpresion != "" {
		// Se recorta porque del otro lado la columna tiene limite y porque un driver puede
		// devolver mensajes larguisimos que no aportan nada despues de la primera linea.
		if len(errorDeImpresion) > 900 {
			errorDeImpresion = errorDeImpresion[:900]
		}
		cuerpo["error"] = errorDeImpresion
	}

	ruta := fmt.Sprintf("print-agent/jobs/%d/resultado", jobID)

	respuesta, codigoHTTP, err := pedir("POST", config.ApiURL, ruta, config.Token, cuerpo)
	if err != nil {
		return err
	}

	if codigoHTTP != 200 {
		return mensajeDeError(respuesta, codigoHTTP)
	}

	return nil
}
