<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logotipo de TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Dale a cada aplicación, modelo y archivo local su propia dirección privada.</strong><br>
  Ábrelos desde otro dispositivo autorizado de tu red Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licencia: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 o posterior"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: nodos tsnet integrados"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 herramientas"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <strong>Español</strong> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Instalación

Necesitas **Go 1.26.6 o posterior** y Git. Aún no se han publicado versiones precompiladas ni un cask de Homebrew; instala desde el código fuente. Estos ejemplos usan **bash o zsh**; consulta la [compatibilidad con plataformas](platforms.md) para los requisitos de Windows y de los servicios en segundo plano.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Usa una cuenta Tailscale con [MagicDNS y HTTPS activados](https://tailscale.com/docs/how-to/set-up-https-certificates). El dispositivo receptor debe estar conectado a tu red Tailscale (**tailnet**), cuya política debe permitirle acceder al servicio. TSLink integra Tailscale en la máquina que publica.

### Comparte tu primera página

Crea una página; TSLink la sirve directamente e inicia su servicio en segundo plano cuando es necesario:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Si TSLink muestra una URL de registro, ábrela para autorizar el nodo; tu tailnet también puede exigir que un administrador apruebe el dispositivo. Después, obtén la dirección exacta:

```bash
tslink url demo --wait
```

Abre esa URL desde un dispositivo autorizado. No necesitas un token de API para esta primera publicación.
[Configuración completa y detalles del ciclo de vida →](getting-started.md)

<a id="use-cases"></a>

## ¿Qué vas a compartir?

Los archivos deben existir; las aplicaciones, bases de datos y backends de modelos ya deben estar en ejecución en los puertos indicados.

| Uso | Comando |
|---|---|
| Abrir una aplicación local desde otro dispositivo | `tslink share 3000` |
| Explorar un directorio de archivos | `tslink share ./public --name files` |
| Leer un informe HTML generado desde el teléfono | `tslink share ./report.html --name report` |
| Conectarse a una base de datos local mediante TCP | `tslink add database --tcp localhost:5432` |
| Usar una API HTTP de modelos locales, como Ollama | `tslink add model --proxy localhost:11434` |

Para Ollama, obtén la URL exacta con `tslink url model --wait`; el `baseURL` de un cliente compatible con OpenAI usa esa URL seguida de `/v1`. [Modelos locales y flujos con datos privados →](local-ai.md)

Para varias apps en un host, TSLink reúne nodos de servicio con nombre, listas de identidades permitidas para HTTP, caducidad de Funnel y gestión por MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) puede bastar para una app en tus propios dispositivos.

<a id="architecture"></a>

## Arquitectura

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Mapa de servicios de ejemplo: App, Docs, Database y Model son nodos con nombres distintos dentro de una misma tailnet. Las aplicaciones, los archivos y las API de modelos usan HTTPS; la base de datos usa TCP privado." width="960">
</picture>

**Una tailnet, nodos de servicio distintos.** Un daemon compartido ejecuta un nodo tsnet integrado por servicio para reenviar HTTP, servir archivos o actuar como proxy TCP. Los cambios del registro surten efecto mientras sigue en ejecución. Cada nodo tiene su propia identidad de red; los servicios comparten la máquina que los publica.
[Detalles de la arquitectura →](architecture.md)

| Componente | Función |
|---|---|
| [Go](../go.mod) | Binario nativo de línea de comandos |
| [Tailscale tsnet](architecture.md) | Nodos de servicio y transporte de la tailnet |
| [Cobra](https://github.com/spf13/cobra) | Comandos y ayuda |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transportes para agentes |
| Llavero del sistema y gestor de servicios de usuario | Credenciales opcionales y ejecución en segundo plano |

Los servicios permanecen en tu tailnet salvo que actives explícitamente [Funnel público](getting-started.md#more-examples). Los servicios HTTP y de archivos admiten listas de identidades autorizadas (`WhoIs`, `--allow`); TCP usa la política de la tailnet y la autenticación del propio backend. Consulta los [límites de publicación](sharing.md).

TSLink no instala apps, no ejecuta modelos, no aísla procesos del host ni agrupa varios hosts. La red, el cifrado y HTTPS provienen de Tailscale; TSLink es un proyecto independiente.

<a id="agents"></a>

## Para agentes

Las **19 herramientas MCP** permiten a un agente compartir informes, gestionar servicios, obtener URL e inspeccionar la configuración. Conecta un cliente MCP local al binario instalado:

```json
{
  "mcpServers": {
    "tslink": {
      "command": "tslink",
      "args": ["mcp"]
    }
  }
}
```

MCP gestiona TSLink; las aplicaciones usan la API HTTP de modelos para la inferencia. Consulta [clientes MCP](mcp-clients.md), [MCP remoto](remote-mcp.md) y la [guía de operación para agentes](../AGENTS.md) para la configuración y automatización.

La automatización de la CLI admite `--json` con `schema_version` igual a `1`; consulta `tslink status --urls --json`. MCP local usa JSON-RPC sobre stdio. Consulta [automatización JSON](json-automation.md).

<a id="roadmap"></a>

## Próximamente

Los elementos En integración, En revisión o Planificado no se incluyen en la instalación desde el código fuente anterior.

| Caso de uso | Estado |
|---|---|
| <!-- roadmap:people --> Da a un familiar acceso a apps HTTP/archivos privadas durante 3 días y reúne las invitaciones en un mensaje; el destinatario sigue necesitando Tailscale. | En integración |
| <!-- roadmap:health --> Comprueba la salud de las apps y recibe alertas de caída o caducidad mediante un comando o webhook opcional. | En integración |
| <!-- roadmap:recipes --> Encuentra apps compatibles en loopback y revisa recetas de apps autoalojadas antes de compartirlas. | En integración |
| <!-- roadmap:limits --> Configura el tamaño de subida y los tiempos de espera de cada app HTTP para cargas grandes y clientes lentos. | En integración |
| <!-- roadmap:windows --> Reinicia un daemon de Windows tras una caída mientras haya sesión iniciada, con una tarea programada y un supervisor integrado. | En integración |
| <!-- roadmap:access-log --> Consulta quién abrió cada app en registros locales, con modos de ruta `prefix`, `full` u `off`. | En revisión |
| <!-- roadmap:portal --> Abre una página de inicio con las apps permitidas y el enlace de registro para propietarios; los visitantes siguen necesitando Tailscale. | En revisión |
| <!-- roadmap:mcp-scopes --> Asigna a un agente un rol y un ámbito de apps, con registros de auditoría de sus cambios. | En revisión |
| <!-- roadmap:guest-links --> Permite a un invitado abrir una app HTTP en el navegador sin instalar Tailscale, con un enlace temporal y PIN opcional, mediante Funnel público con control de acceso. | En revisión |
| <!-- roadmap:durations --> Elige duraciones predefinidas o personalizadas de al menos 1 hora, con un máximo para invitados de 7 días por defecto, configurable. | En revisión |
| <!-- roadmap:requests --> Ayuda a usuarios de móviles a unirse con un código QR y aprueba acceso a apps o más tiempo en una acción. | En revisión |
| <!-- roadmap:multi-host --> Consulta apps de varios hosts en un inventario. | Planificado |

<a id="documentation"></a>

## Documentación y licencia

[Primeros pasos](getting-started.md) · [Modelos locales](local-ai.md) ·
[Referencia de la CLI](cli-reference.md) · [Plataformas](platforms.md) · [Hoja de ruta](roadmap.md)

Contribuye siguiendo [CONTRIBUTING.md](../CONTRIBUTING.md); informa de vulnerabilidades mediante [SECURITY.md](../SECURITY.md).

TSLink usa la [licencia Apache 2.0](../LICENSE) sin modificaciones, incluido el uso comercial. Conserva el [NOTICE](../NOTICE) aplicable y los [avisos de terceros](../THIRD_PARTY_NOTICES.md) al redistribuirlo. La [cooperación comercial](../COMMERCIAL.md) es voluntaria y no añade condiciones a la licencia. Los términos de servicio y planes de Tailscale se aplican por separado.
