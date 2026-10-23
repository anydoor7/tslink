<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink es una CLI y un servidor MCP de código abierto que da a cada aplicación de tu ordenador una dirección privada <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, para que tú o tu coding agent podáis abrir en el teléfono un servidor de desarrollo en ejecución. Proyecto independiente, no creado por Tailscale.</p>
<p align="center">Tu teléfono necesita la app de Tailscale, con la sesión iniciada en la misma tailnet.</p>
<p align="center"><a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <strong>Español</strong> · <a href="INDEX.md#translated-homepages">Más idiomas</a></p>

```text
# Claude Code, tras instalar la CLI (ver Instalación más abajo)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Clientes MCP locales que usan JSON con mcpServers (otros: consulta la configuración MCP de tu cliente)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Dile a tu agente: "Comparte el servidor de desarrollo para que pueda abrirlo en el teléfono."
```

Lo mismo, a mano, con el servidor de desarrollo ya escuchando en el puerto 5173. Con `--json`, `share` informa del nombre registrado en `data.name`. Usa ese nombre en los comandos siguientes: difiere de `--name` cuando ese nombre ya estaba en uso, por ejemplo `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, o data.auth_url (enlace de inicio de sesión de Tailscale) si status es needs_login
tslink url <name> --wait              # cuando termines de iniciar sesión: la URL exacta
tslink remove <name>                  # al terminar; omítelo si share falló. El servidor de desarrollo sigue en marcha
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalación

Los equipos macOS requieren macOS 13 Ventura o posterior ([plataformas compatibles](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

En Windows, instala con Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Para actualizar, ejecuta `brew upgrade --cask tslink` o `scoop update; scoop update tslink`. Si TSLink se ejecuta como servicio en segundo plano, vuelve a ejecutar `tslink install` después.

Los paquetes `.deb` y `.rpm` para Linux y las versiones para Windows están en la [última versión](https://github.com/anydoor7/tslink/releases/latest). La primera vez que compartes una aplicación, TSLink muestra un enlace de inicio de sesión de Tailscale para ella. [Primeros pasos](getting-started.md)

<a id="agents"></a>

## Para agentes de IA

Un servidor de desarrollo que un agente arranca en `localhost` queda fuera del alcance de tu teléfono. TSLink permite que el agente le dé una dirección privada, te entregue la URL exacta y quite el recurso compartido al terminar. Tu teléfono necesita la app de Tailscale, con la sesión iniciada en la misma tailnet.

Probado el 2026-10-22 con Vite 8.3.5 y un iPhone 15 Pro: el agente compartió el servidor de desarrollo por MCP, el teléfono abrió la URL privada y la página se actualizó al guardar, sin recargar a mano.

Para Claude Code, instala primero la CLI y ejecuta después:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

El plugin incluye una [skill de vista previa](../skills/tslink-preview/SKILL.md) y la conexión MCP que aparece más abajo. El marketplace está en este repositorio y obtiene el plugin de [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). La skill de referencia vive aquí; el plugin lleva una copia verificada por CI. Otros agentes pueden usar la misma skill o conectarse directamente por MCP. Esta integración requiere la CLI local y no funciona en el chat de claude.ai. [Configuración y alta](agent-integrations.md)

Para un cliente MCP local que usa JSON con `mcpServers` (otros clientes: consulta sus instrucciones de configuración MCP y la [guía de clientes MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Un nombre por servidor de desarrollo.** Cada recurso compartido tiene su propia dirección privada `https://<name>.<tailnet>.ts.net`, en la ruta raíz y el puerto 443. Si el nombre ya está en uso, se le añade un sufijo numérico, así que usa la URL que recibe el agente.
- **Cada aplicación nueva necesita a una persona.** En la configuración predeterminada, cada nodo de aplicación nuevo requiere que una persona complete el alta en Tailscale. Si el resultado es `needs_login`, termina de iniciar sesión y ejecuta `tslink url <name> --wait` para obtener la URL exacta. Aparte, un nombre nuevo puede tardar en estar accesible: de 36 a 38 segundos en nuestras dos pruebas.
- **Quién puede abrirlo.** Por defecto, los dispositivos de la tailnet que tu política permita. `share` de MCP acepta `allow` para limitarlo a tu cuenta; desde la CLI, usa `tslink add web --proxy localhost:5173 --allow you@example.com`. Los nombres aparecen en los registros públicos de certificados, así que no son secretos.
- **Editas, guardas y lo ves.** Al guardar el código, la página se actualiza en tu teléfono. Las reglas de Host y Origin de tu propia aplicación aún pueden bloquear peticiones: en nuestra prueba Vite no necesitó cambiar `allowedHosts`; Next.js no se ha probado y sigue necesitando `allowedDevOrigins`.
- **Limpieza.** El agente llama a `unshare`, o tú ejecutas `tslink remove <name>`, siempre con el nombre que devolvió el resultado de share. La eliminación anula el registro del recurso compartido e intenta borrar su nodo de la tailnet cuando TSLink puede demostrar que es suyo, así que revisa en el resultado los campos de limpieza o de estado conservado. No detiene el servidor de desarrollo.
- **CLI o MCP.** Los comandos de gestión aceptan `--json` y devuelven resultados versionados; `tslink mcp` ofrece operaciones de aplicaciones y acceso por MCP.
- **Un archivo, no la carpeta.** Un agente puede compartir solo su informe HTML autocontenido con `tslink share ./report.html`; los demás archivos de esa carpeta siguen inaccesibles.
- **Roles limitados.** `viewer`, `app-operator` o `people-manager`, limitados a las aplicaciones que indiques; compartir una aplicación nueva requiere el rol `owner`. `tslink mcp-audit` muestra qué cambió un agente. Los roles limitan las herramientas de TSLink, no la propia shell del agente.

[Guía para agentes](agent-quickstart.md) · [Permisos MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

<a id="why"></a>

## Cuándo necesitas TSLink

Serve basta para una aplicación que arrancas tú y abres en tu propio teléfono. TSLink está pensado para los servidores de desarrollo que arranca un agente, a menudo varios, cuando cada uno debe tener su propio nombre privado y los pasos de compartir, obtener la URL y limpiar vienen resueltos en la CLI, MCP y una skill de Claude Code.

| Tarea | Solo Tailscale | TSLink |
|---|---|---|
| Varios servidores de desarrollo que arranca tu agente, un nombre cada uno | Serve define rutas a nivel de dispositivo bajo un nombre por máquina; Services exige para cada nombre una definición, una aprobación y un host con etiquetas | Un nodo por aplicación, con compartir, obtener la URL y limpiar integrados en la CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) y una skill |
| Una aplicación que arrancas tú, en tu teléfono | Basta con `tailscale serve 3000` | `tslink share 3000` |
| Una persona, una aplicación, siete días | Reglas de política y luego una herramienta JIT o retirarlas a mano | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/archivos) |
| Enlace para navegador, tres días | Funnel público; añadir un control de acceso y un apagado programado | `tslink guest create photos --for 3d --public --print-link` (solo HTTP) |

Los destinatarios privados necesitan Tailscale. Los enlaces de invitado son públicos, se pueden reenviar y sirven como credenciales de acceso.

[Comparación completa](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Tus aplicaciones, en tus propios dispositivos

- **Una dirección para cada aplicación.** Aplicaciones web, carpetas, archivos sueltos y puertos TCP reciben cada uno su propio nombre en tu tailnet, así que usas nombres en lugar de direcciones IP.
- **Privado por defecto.** Nada es público hasta que creas un enlace de invitado o publicas con Funnel.
- **Una aplicación, no toda la máquina.** Cada aplicación que publicas tiene su propio nodo, que reenvía solo a esa aplicación. Sin la app de Tailscale en el host, TSLink no añade otros puertos del host a tu tailnet.
- **Una página de inicio** que lista tus aplicaciones con su estado. [Portal](portal.md)
- **Comprobaciones de estado y alertas** por comando o webhook, y un registro de acceso que incluye las solicitudes denegadas. [Estado y alertas](health-and-alerts.md) · [Historial de acceso](access-log.md)
- **Recetas para 15 aplicaciones autoalojadas**, entre ellas Home Assistant, Jellyfin, Immich y Ollama. `tslink apps detect` encuentra las que ya están en marcha. [Recetas de aplicaciones](apps.md)

## Comparte cuando quieras

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Los enlaces de invitado y las nuevas URL públicas caducan y solo sirven para aplicaciones web; carpetas, archivos y puertos TCP siguen siendo privados. [Personas](people.md) · [Enlaces de invitado](guest-links.md) · [Acceso público](funnel.md)

<a id="architecture"></a>

## Cómo funciona

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC o servidor en la nube: CLI/MCP gestiona un demonio común y nodos por aplicación. Los dispositivos privados usan Tailscale cifrado; HTTPS/Funnel público opcional llega a aplicaciones HTTP mediante control de invitados o publicación abierta explícita." width="960">
</picture>

Un proceso en segundo plano ejecuta un nodo de Tailscale independiente para cada aplicación. Tailscale aporta el transporte de la tailnet y los certificados HTTPS. El acceso privado a web y archivos se puede limitar por identidad de Tailscale con `--allow` y permisos por persona; el TCP sin procesar depende de la política de tu tailnet y del inicio de sesión propio de la aplicación. [Arquitectura](architecture.md)

<a id="requirements"></a>

## Requisitos

| Quién | Necesita |
|---|---|
| Tú | Una cuenta de Tailscale con MagicDNS y HTTPS activados |
| La máquina donde corren tus aplicaciones | TSLink, que incluye Tailscale (en Linux, una sesión de usuario de systemd) |
| Tus dispositivos y las personas con quienes compartes | La app de Tailscale |
| Invitados | Un navegador |

Los nombres de las aplicaciones HTTPS aparecen en registros públicos de certificados, así que elige nombres que no te importe que otros vean.

<a id="documentation"></a>

## Más

[Toda la documentación](INDEX.md) · [Referencia CLI](cli-reference.md) · [Comparación con Serve, ngrok y Cloudflare](comparison.md) · [Contribuir](../CONTRIBUTING.md) · [Seguridad](../SECURITY.md)

Apache 2.0. TSLink es un proyecto independiente, no creado ni respaldado por Tailscale.
