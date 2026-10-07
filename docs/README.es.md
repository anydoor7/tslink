<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Direcciones privadas para tus aplicaciones, en tu red Tailscale.</strong></p>
<p align="center">Ábrelas desde tus propios dispositivos. Comparte una con una persona o con un enlace, hasta la fecha que elijas.</p>
<p align="center"><a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <strong>Español</strong> · <a href="INDEX.md#translated-homepages">Más idiomas</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalación

```sh
brew install --cask anydoor7/tap/tslink
```

Los paquetes `.deb` y `.rpm` para Linux y las versiones para Windows están en la [última versión](https://github.com/anydoor7/tslink/releases/latest). La primera vez que compartes una aplicación, TSLink muestra un enlace de inicio de sesión de Tailscale para ella. [Primeros pasos](getting-started.md)

<a id="why"></a>

## Cuándo necesitas TSLink

Para una aplicación en tus propios dispositivos, Serve es suficiente. TSLink reúne direcciones de aplicaciones, plazos y cambios de acceso en un solo flujo.

| Tarea | Solo Tailscale | TSLink |
|---|---|---|
| Una aplicación web en tu móvil | Basta con `tailscale serve 3000` | `tslink share 3000` |
| Varias aplicaciones, cada una con su nombre | Configurar Services, o nodos separados | Un `share`/`add` por aplicación; registrar cada nodo |
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

<a id="agents"></a>

## Para agentes de IA

Un servidor de desarrollo que un agente arranca en `localhost` queda fuera del alcance de tu teléfono. TSLink permite que el agente le dé una dirección privada, informe la URL exacta y la quite al terminar.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI o MCP.** Los comandos de gestión aceptan `--json` y devuelven resultados versionados; `tslink mcp` ofrece operaciones de aplicaciones y acceso por MCP.
- **Un archivo, no la carpeta.** Un agente puede compartir solo su informe HTML con `tslink share ./report.html`; los demás archivos de esa carpeta siguen inaccesibles.
- **Roles limitados.** `viewer`, `app-operator` o `people-manager`, limitados a las aplicaciones que indiques. `tslink mcp-audit` muestra qué cambió un agente. Los roles limitan las herramientas de TSLink, no la propia shell del agente.

[Guía para agentes](agent-quickstart.md) · [Permisos MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

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
