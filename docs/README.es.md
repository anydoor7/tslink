<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logotipo de TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Comparte las aplicaciones de tu ordenador con quien tú elijas, durante el tiempo que tú decidas.</strong><br>
  Cada aplicación tiene su propia dirección privada en tu red Tailscale. Consulta quién tiene acceso y retíralo cuando quieras.
</p>

<p align="center">
  <a href="#quickstart">Inicio rápido</a> · <a href="#agents">Para agentes</a> · <a href="getting-started.md">Documentación</a> ·
  <strong>Español</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Todos los idiomas</a>
</p>

## Para qué se usa

- **Abre tu trabajo en el móvil.** Un informe generado por tu script, un servidor de desarrollo, un cuaderno o una API de un modelo local, en una dirección HTTPS privada accesible desde dispositivos autorizados.
- **Da acceso temporal a una aplicación a una persona.** Deja que tu pareja use la biblioteca de fotos una semana o que un colega pruebe tu versión preliminar tres días. El acceso caduca automáticamente; también puedes terminarlo antes.
- **Deja que tu agente se encargue de compartir.** Tu agente de programación acaba de crear un panel. Pídele que lo comparta contigo y con tu compañero hasta el viernes. También puede decirte qué está compartido y retirar el acceso.

Tus aplicaciones siguen ejecutándose donde ya estaban. TSLink controla quién puede acceder a cada una y mantiene una lista de qué se comparte, con quién y hasta cuándo.

<a id="quickstart"></a>

## Inicio rápido

Necesitas **Go 1.26.6+**, Git y una cuenta Tailscale con [MagicDNS y HTTPS activados](https://tailscale.com/docs/how-to/set-up-https-certificates). Aún no se publican versiones precompiladas, así que instala desde el código fuente:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Comparte una página:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

La primera vez, TSLink muestra un enlace de inicio de sesión para registrar el nuevo nodo de servicio; tu tailnet también puede exigir que un administrador apruebe el dispositivo. Tras el registro, abre la URL del servicio en un dispositivo autorizado conectado a tu tailnet. No necesitas un token de API.

Comprueba qué se comparte y elimina la demostración:

```bash
tslink status --urls
tslink remove demo
```

También puedes compartir lo siguiente cuando su backend esté en marcha:

| Qué | Comando |
|---|---|
| Una aplicación web local | `tslink share 3000` |
| Una carpeta de archivos | `tslink share ./public --name files` |
| Una API de modelo local, como Ollama | `tslink add model --proxy localhost:11434` |
| Una base de datos por TCP privado | `tslink add database --tcp localhost:5432` |
| Una aplicación autoalojada conocida (Jellyfin, Immich, Home Assistant y otras 13) | `tslink apps detect`, después `tslink apps share jellyfin --yes` |

[Primeros pasos, plataformas y servicio en segundo plano →](getting-started.md)

## Elige quién puede acceder

| Destinatarios | Qué necesitan | Su identidad | Fin del acceso |
|---|---|---|---|
| **Tus propios dispositivos** | Sesión iniciada en tu tailnet | Identidad Tailscale verificada | Cuando eliminas la aplicación |
| **Personas concretas** (HTTP/archivos privados) | Una cuenta Tailscale; quienes estén fuera aceptan una invitación por aplicación | Identidad Tailscale verificada | Al cumplirse el plazo indicado (`--for 7d`) o con `tslink people remove` |
| **Cualquiera con la URL** (Funnel) | Un navegador | Cualquier persona; se mantiene el inicio de sesión de la aplicación | A las 24 horas por defecto (`--funnel-ttl`) |
| **Enlace de invitado para navegador** *(próximamente)* | Un navegador y, opcionalmente, un PIN | Quien tenga el enlace | Al caducar o revocarse |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Los plazos de HTTP y archivos privados se comprueban en cada solicitud. Revocar el acceso impide nuevas solicitudes; no recupera datos descargados ni cierra flujos o conexiones WebSocket ya aceptados. [Compartir con personas →](people.md) · [Límites del uso compartido →](sharing.md)

<a id="agents"></a>

## Para agentes

TSLink incluye un servidor MCP para que un agente pueda compartir, listar, explicar y eliminar accesos igual que tú. Añádelo a un cliente MCP local:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Resultados exactos.** La automatización por CLI admite `--json` con `schema_version: 1` y códigos de error estables; `tslink mcp` utiliza JSON-RPC. `tslink manifest` describe todos los comandos y opciones. Los agentes deben obtener las URL reales mediante `tslink url <name> --wait` en lugar de construirlas.
- **Estados de espera claros.** Un nodo nuevo que todavía requiere que una persona inicie sesión informa `needs_login`, sin fingir que está listo.
- **Permisos.** MCP local se ejecuta con los permisos de tu usuario. MCP remoto se activa expresamente, solo es accesible dentro de la tailnet y se limita a las identidades o etiquetas indicadas. Los roles por agente, ámbitos de aplicaciones y comprobantes de acciones llegarán *próximamente*.

El MCP de TSLink opera sobre TSLink. Si publicas otro servidor MCP a través de TSLink, ese servidor sigue necesitando sus propios permisos de herramientas.
[Guía para agentes →](agents.md) · [Clientes MCP →](mcp-clients.md) · [MCP remoto →](remote-mcp.md) · [Automatización JSON →](json-automation.md)

## Cuándo usar otra herramienta

| Si quieres | Considera |
|---|---|
| Un servicio local en tus dispositivos con el cliente Tailscale que ya utilizas | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Servicios administrados con nombres estables entre varios hosts | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Una URL pública para un webhook o una demostración de API sin cuenta Tailscale | [ngrok](https://ngrok.com/docs/start) o [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Instalar y ejecutar aplicaciones autoalojadas, además de compartirlas | [Umbrel](https://umbrel.com) o [Coolify](https://coolify.io) |
| Una plataforma de acceso basada en identidad para toda la organización | [Pangolin](https://github.com/fosrl/pangolin) o [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink encaja cuando una persona ejecuta varias aplicaciones y quiere acceso temporal por aplicación y persona que tanto ella como su agente puedan consultar.

## Cómo funciona

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database y Model son nodos separados con nombre propio dentro de una tailnet, ejecutados por un único daemon TSLink en el ordenador que publica los servicios." width="720">
</picture>

Un daemon en segundo plano ejecuta un nodo Tailscale integrado por aplicación, con nombre y dirección propios. Para HTTP y archivos privados, `WhoIs` y los permisos por persona o las reglas `--allow` controlan el acceso; los plazos se comprueban en cada solicitud. TCP sin procesar usa la política de la tailnet y la autenticación del backend. Tailscale proporciona el transporte de la tailnet, el cifrado y los certificados; TSLink es un proyecto independiente. Todas las aplicaciones comparten el ordenador que las publica; TSLink no las aísla entre sí. [Arquitectura →](architecture.md)

## Estado

Ya disponible: direcciones privadas por aplicación, personas con plazos y paquetes de invitaciones, Funnel público con caducidad, comprobaciones de salud y alertas, recetas de aplicaciones autoalojadas, límites de solicitudes por aplicación, reinicio tras fallos en Windows, CLI y MCP.

Próximamente: enlaces de invitado para navegador, duraciones flexibles, registro de acceso, una página principal de tus aplicaciones, roles de agente limitados, incorporación mediante QR y solicitudes de acceso. Está previsto reunir varios ordenadores en una lista. [Hoja de ruta →](roadmap.md)

## Documentación y licencia

[llms.txt](../llms.txt) · [Inicio rápido para agentes](agent-quickstart.md) · [Elegir una herramienta para compartir](comparison.md)

[Primeros pasos](getting-started.md) · [Referencia CLI](cli-reference.md) · [Plataformas](platforms.md) · [Modelos locales](local-ai.md) · [Contribuciones](../CONTRIBUTING.md) · [Seguridad](../SECURITY.md)

Apache License 2.0, incluido el uso comercial. Conserva [NOTICE](../NOTICE) y los [avisos de terceros](../THIRD_PARTY_NOTICES.md) al redistribuir. Las condiciones y los planes de Tailscale se aplican por separado.
