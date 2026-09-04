package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config es lo unico que el agente guarda en disco: con que servidor hablar y con que token.
//
// No guarda nada del comercio ni de las ventas. Si alguien copia este archivo a otra maquina,
// lo unico que consigue es que esa maquina reciba los tickets de esa caja -- por eso el archivo
// vive bajo el perfil del usuario y no en una carpeta compartida.
type Config struct {
	ApiURL       string `json:"api_url"`
	Token        string `json:"token"`
	NombreEquipo string `json:"nombre_equipo"`
}

// carpetaDeDatos es donde viven la config y el log: %APPDATA%\ComercioCityPrint.
//
// Se usa APPDATA y no la carpeta del ejecutable a proposito: el .exe se puede mover o borrar
// (tipicamente el que quedo en Descargas) sin que el equipo pierda la vinculacion.
func carpetaDeDatos() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errNoAppData
	}

	carpeta := filepath.Join(appData, "ComercioCityPrint")

	if err := os.MkdirAll(carpeta, 0700); err != nil {
		return "", err
	}

	return carpeta, nil
}

// rutaDeConfig devuelve la ruta completa del config.json.
func rutaDeConfig() (string, error) {
	carpeta, err := carpetaDeDatos()
	if err != nil {
		return "", err
	}

	return filepath.Join(carpeta, "config.json"), nil
}

// leerConfig devuelve la configuracion guardada, o nil si el equipo todavia no se vinculo.
func leerConfig() (*Config, error) {
	ruta, err := rutaDeConfig()
	if err != nil {
		return nil, err
	}

	contenido, err := os.ReadFile(ruta)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(contenido, &config); err != nil {
		return nil, err
	}

	// Una config a medias es lo mismo que no tener config: mejor volver a vincular que quedar
	// sondeando para siempre contra una URL vacia.
	if config.ApiURL == "" || config.Token == "" {
		return nil, nil
	}

	return &config, nil
}

// guardarConfig escribe la configuracion con permisos de solo-el-usuario.
func guardarConfig(config *Config) error {
	ruta, err := rutaDeConfig()
	if err != nil {
		return err
	}

	contenido, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(ruta, contenido, 0600)
}
