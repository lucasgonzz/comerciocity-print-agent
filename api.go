package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errNoAppData = errors.New("no se pudo ubicar la carpeta de datos del usuario (APPDATA)")

	// ErrNoAutorizado es terminal: el equipo fue desvinculado desde el sistema y el token no se
	// arregla solo. Se distingue del resto de los errores para poder cortar el bucle en vez de
	// sondear para siempre contra un servidor que va a seguir contestando 401.
	ErrNoAutorizado = errors.New("el equipo ya no esta vinculado")
)

// clienteHTTP tiene timeout a proposito: sin el, un servidor que acepta la conexion y no contesta
// deja al agente colgado para siempre y la caja deja de imprimir sin que nadie sepa por que.
//
// 🔴 CheckRedirect corta los redirects que cambian de host. El header X-Print-Agent-Token NO esta
// en la lista de headers sensibles que Go descarta al redirigir cross-domain (solo saca
// Authorization, Cookie y Proxy-*), asi que sin esto un 302 se llevaria el token del equipo a
// otro dominio.
var clienteHTTP = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}

		if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return errors.New("el servidor intento redirigir a otro dominio")
		}

		if len(via) >= 5 {
			return errors.New("demasiados redirects")
		}

		return nil
	},
}

// Job es un ticket esperando salir por la comandera.
type Job struct {
	ID            int    `json:"id"`
	PrinterName   string `json:"printer_name"`
	PayloadBase64 string `json:"payload_base64"`
}

// ValidarApiURL exige que la direccion sea https y tenga host.
//
// 🔴 Es una guarda de seguridad, no una validacion de formato. El codigo de vinculacion lleva
// adentro la URL del servidor, y la pantalla del sistema entrena al operador a pegar codigos sin
// mirarlos: un codigo armado a mano apuntaria el agente a cualquier host, que le devolveria
// ESC/POS arbitrario para imprimir en la comandera del comercio. Sin QZ de por medio ya no hay
// ningun cartel de permiso que lo frene, asi que el filtro tiene que estar aca.
func ValidarApiURL(crudo string) error {
	direccion, err := url.Parse(crudo)
	if err != nil {
		return errors.New("la direccion del sistema no es valida")
	}

	if direccion.Scheme != "https" {
		return errors.New("la direccion del sistema tiene que ser https")
	}

	if direccion.Host == "" {
		return errors.New("la direccion del sistema no tiene servidor")
	}

	return nil
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

	direccion := strings.TrimRight(apiURL, "/") + "/api/" + strings.TrimLeft(ruta, "/")

	req, err := http.NewRequest(metodo, direccion, lector)
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
	if status == 401 {
		return ErrNoAutorizado
	}

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
//
// 🔴 Reintenta, y no es opcional. Si este aviso se pierde, el trabajo queda en "tomado" del lado
// del servidor: el ticket ya salio por la comandera pero el sistema no lo sabe. El backend barre
// los "tomado" viejos y los vuelve a entregar, asi que perder el aviso termina reimprimiendo un
// ticket que ya habia salido.
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

	var ultimoError error

	for intento := 0; intento < 4; intento++ {
		if intento > 0 {
			time.Sleep(time.Duration(intento*intento) * time.Second)
		}

		respuesta, codigoHTTP, err := pedir("POST", config.ApiURL, ruta, config.Token, cuerpo)

		if err == nil && codigoHTTP == 200 {
			return nil
		}

		if err != nil {
			ultimoError = err
			continue
		}

		ultimoError = mensajeDeError(respuesta, codigoHTTP)

		// Un 401 no mejora reintentando.
		if ultimoError == ErrNoAutorizado {
			return ultimoError
		}
	}

	return ultimoError
}
