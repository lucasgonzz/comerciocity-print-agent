# ComercioCity — Agente de impresión

Programa que conecta la comandera térmica de un comercio con el sistema de ComercioCity.
Corre en la computadora de la caja, se instala una vez y arranca solo con Windows.

## Para un comercio

**No hace falta leer esto.** El sistema te da el botón de descarga y las instrucciones paso a paso
en la pantalla de configuración de impresión, dentro del menú **Imprimir** de una venta.

## Qué hace

Recibe los tickets del sistema y los manda a la impresora en ESC/POS crudo, sin generar un PDF ni
pasar por el diálogo de impresión de Windows.

La diferencia con QZ Tray, que es lo que reemplaza: **el agente sale hacia el servidor** en vez de
escuchar en un puerto local esperando que el navegador le hable. Eso evita de una sola vez:

- el cartel de permiso que QZ muestra en cada impresión,
- el permiso de **Local Network Access** que Chrome 141 introdujo sobre `localhost`,
- el bloqueo por mixed content de una página HTTPS contra un servidor local,
- tener que abrir puertos en la red del comercio.

Y habilita algo que antes no se podía: mandar a imprimir **desde otro dispositivo**, porque quien
imprime ya no necesita estar sentado en la máquina que tiene la impresora enchufada.

## Cómo funciona

```
Sistema → cola de trabajos en la API del comercio ← sondeo cada 2s ← Agente → spooler RAW → comandera
```

- **Vinculación:** el sistema genera un código que lleva adentro la dirección de la API. El
  operador lo copia de un botón y lo pega en el agente. El agente lo canjea por un token
  permanente del equipo y guarda `%APPDATA%\ComercioCityPrint\config.json`.
- **Impresión:** el agente pregunta cada 2 segundos si hay tickets (cada 8 segundos si hace más de
  3 minutos que no imprime nada). Los recibe en base64, los manda por `winspool.drv` con el tipo de
  datos `RAW`, e informa cómo salió cada uno.
- **Arranque automático:** se copia a `%LOCALAPPDATA%\ComercioCityPrint\` y deja una copia en la
  carpeta de inicio de Windows. **No requiere permisos de administrador.**

### Por qué el spooler y no USB directo

En Windows, el driver `usbprint.sys` reclama la impresora en exclusiva. WebUSB y cualquier acceso
USB crudo fallan salvo que se reemplace el driver con Zadig, y eso deja la impresora inutilizable
para todo lo demás. El spooler con `Datatype: RAW` pasa los bytes tal cual sin renderizar nada, que
es exactamente lo que hace falta, y es el camino soportado.

## Compilar

Sin dependencias: solo la biblioteca estándar de Go.

```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o ComercioCityPrint.exe .
```

```bash
go test ./...
```

Los tests de impresoras corren solo en Windows: consultan las impresoras reales de la máquina para
verificar que las llamadas a `EnumPrintersW` estén bien armadas — un error de tamaño de struct ahí
no da error, devuelve basura.

## Publicar una versión

Un tag `vX.Y.Z` dispara el workflow, que compila y publica el ejecutable. El sistema descarga
siempre desde el mismo link, así que no hay que tocar nada más:

```
https://github.com/lucasgonzz/comerciocity-print-agent/releases/latest/download/ComercioCityPrint.exe
```

## Lo que todavía no tiene

- **Firma de código.** El ejecutable va sin firmar, así que Windows muestra SmartScreen la primera
  vez ("Windows protegió su PC" → *Más información* → *Ejecutar de todas formas"). Las
  instrucciones del sistema lo explican, porque es donde más gente abandona.
- **Actualización automática.** Por ahora, para actualizar se descarga la versión nueva y se
  ejecuta.
