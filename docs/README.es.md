<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Da a cada aplicación de tu ordenador o servidor su propia dirección privada en tu red de Tailscale, y decide quién puede acceder a ella.</strong></p>

Abre tus aplicaciones web, carpetas, API de modelos y bases de datos desde tu propio móvil y portátil, con comprobaciones de estado e historial de acceso para cada una. Tus agentes de IA pueden dar a las aplicaciones que inician en localhost una dirección privada para tus otros dispositivos y comprobarlas, dentro del rol que les des. Cuando otra persona necesite entrar, concede acceso a alguien concreto hasta una fecha o abre una aplicación web a internet durante un tiempo limitado.

**Requiere Tailscale.** Necesitas una cuenta de Tailscale (gratis para uso personal), y cada dispositivo que abra una aplicación privada necesita la app de Tailscale; los invitados y visitantes públicos solo necesitan un navegador. TSLink es un proyecto independiente, no creado ni respaldado por Tailscale. [Requisitos](#requirements)

<p align="center"><a href="#quickstart">Inicio rápido</a> · <a href="#agents">Para agentes</a> · <a href="comparison.md">Comparación con Serve, ngrok y Cloudflare</a> · <a href="#documentation">Documentación</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <strong>Español</strong> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Qué puedes hacer

### Acceder a tus propias aplicaciones

- **Una dirección para cada aplicación.** `tslink share 3000`, `tslink share ./photos` o `tslink add db --tcp localhost:5432` da a una aplicación web, carpeta, archivo o servicio TCP su propia dirección privada en tu tailnet (tu red privada de Tailscale), como `https://photos.<tailnet>.ts.net`. Cada aplicación es un dispositivo de Tailscale independiente, así que abres las aplicaciones por su nombre en lugar de por dirección IP y puerto.
- **Privadas salvo que decidas otra cosa.** TSLink mantiene privado por defecto el acceso a las aplicaciones, y la política de tu tailnet decide qué dispositivos pueden conectarse. Solo abre un punto de acceso público cuando creas un enlace de invitado o publicas expresamente a través de Funnel.
- **Todo en un solo lugar.** `tslink status --urls` lista las aplicaciones registradas en este ordenador, y una página de inicio privada opcional muestra sus direcciones y su estado. [Portal](portal.md)
- **Entérate cuando algo falla.** Las comprobaciones de estado en segundo plano pueden avisarte mediante un comando o un webhook cuando una aplicación se cae o vuelve, o cuando su inicio de sesión de Tailscale está a punto de caducar. El historial de acceso muestra quién abrió qué aplicación y cuándo, incluidas las solicitudes denegadas. [Estado y alertas](health-and-alerts.md) · [Historial de acceso](access-log.md)
- **Recetas de aplicaciones.** Hay recetas para 15 aplicaciones autoalojadas, entre ellas Home Assistant, Jellyfin, Immich y Ollama, y `tslink apps detect` puede encontrar aplicaciones compatibles que ya escuchan en local. Para subir fotos y vídeos grandes, [aumenta los límites de subida de cada aplicación](sharing.md). [Recetas de aplicaciones](apps.md) · [IA local](local-ai.md)

### Deja que tus agentes trabajen con ellas

Cuando un agente arranca un servidor de desarrollo, una vista previa o una API de modelo local en `localhost`, tu móvil y tus otros ordenadores no pueden llegar a esa dirección. TSLink permite que el agente le dé una dirección privada, te diga la URL exacta y después quite su registro, dentro de los límites que fijes.

- **Compartir, comprobar, deshacer.** `share --json` devuelve el nombre que registró y, además, la URL exacta o un enlace de inicio de sesión para que lo abras. `url <name> --wait` y `status --urls --name <name>` indican si el punto de acceso está listo, y `remove <name>` (`unshare` en MCP) retira lo compartido. [Guía para agentes](agent-quickstart.md)
- **Pensado para automatizar.** Los comandos de gestión, salvo `tslink mcp`, aceptan `--json` y devuelven resultados versionados con códigos de error estables. `tslink mcp` ofrece herramientas de aplicaciones y de acceso a un cliente MCP local mediante JSON-RPC. Con las asignaciones de llamantes configuradas, `tslink serve --mcp` ofrece esas herramientas a los clientes MCP de tus otros dispositivos a través del tailnet. [Automatización JSON](json-automation.md) · [MCP remoto](remote-mcp.md)
- **Autoridad limitada.** Un agente local tiene autoridad de propietario por defecto. Dale a un agente un rol reducido (`viewer`, `app-operator` o `people-manager`) que cubre solo las aplicaciones que indiques y limita cuánto puede durar cualquier acceso que conceda. Los cambios hechos por MCP quedan registrados, y `tslink mcp-audit` los muestra. Los roles limitan las herramientas de TSLink, no la shell ni los archivos propios del agente. [Permisos MCP](mcp-scopes.md)

### Comparte con las personas que elijas

- **Personas concretas, hasta una fecha.** `tslink people add alice@example.com --apps photos,notes --for 7d` permite que ese inicio de sesión de Tailscale abra esas aplicaciones web y de archivos hasta la fecha límite. `people update`, `extend` y `people remove` lo cambian o lo terminan; al quitar a alguien se deniega su siguiente solicitud, pero no se puede recuperar lo que ya descargó. [Personas](people.md) · [Duraciones](durations.md)
- **Alguien fuera de tu tailnet.** Añade `--invite --print-links` para obtener un mensaje listo para enviar con una invitación de dispositivo para cada aplicación (requiere un token de API que pertenezca a un usuario). `--qr` imprime un código para configurar el móvil.
- **Solicitudes.** Las personas de tu tailnet pueden pedir más tiempo, o acceso a una aplicación que marques como solicitable, desde la página de inicio. Apruebas con una duración en un solo comando. [Solicitudes de acceso](requests.md)

### Abre una aplicación web a internet durante un tiempo

- **Enlaces de invitado.** `tslink guest create photos --for 3d --public --print-link` crea un enlace de navegador a una aplicación web, con PIN opcional, que puedes revocar por separado. Los invitados no necesitan cuenta de Tailscale. Cualquiera que tenga el enlace puede usarlo, así que no demuestra quién lo visitó. [Invitados](guest-links.md)
- **Una URL pública abierta.** `tslink add preview --proxy localhost:3000 --funnel --public` publica una aplicación web para cualquiera que tenga su URL. Una publicación nueva dura 24 horas por defecto; usa `--funnel-ttl` para elegir otra duración. [Funnel](funnel.md)
- Los enlaces de invitado nuevos y las publicaciones públicas abiertas nuevas usan Tailscale Funnel y tienen una duración limitada (mínimo 1 hora, máximo predeterminado de 7 días, configurable por el propietario). Estas vías públicas admiten aplicaciones con proxy HTTP; los servicios directos de carpetas o archivos y el TCP sin procesar siguen siendo privados.

Todo lo anterior se incluye en v0.1.0.

<a id="requirements"></a>

## Requisitos

TSLink está construido sobre Tailscale. Es un proyecto independiente, no creado ni respaldado por Tailscale, y se aplican los términos y [planes](https://tailscale.com/pricing) propios de Tailscale.

| Quién | Qué necesita |
|---|---|
| Tú | Una cuenta de Tailscale con [MagicDNS y HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) activados. El plan Personal gratuito es para uso no comercial. |
| El ordenador o servidor donde se ejecutan tus aplicaciones | Solo TSLink. Lleva Tailscale integrado, así que no hace falta instalar Tailscale aparte. Con la configuración predeterminada, cada nodo de aplicación nuevo necesita iniciar sesión en el navegador y puede necesitar la aprobación del dispositivo. Las [credenciales guardadas](credentials-and-tags.md) permiten darlos de alta sin iniciar sesión en el navegador para cada aplicación. |
| Tus otros dispositivos | La app de Tailscale, con sesión iniciada en tu tailnet. |
| Las personas que elijas | La app de Tailscale y su propio inicio de sesión. O se unen a tu tailnet, lo que añade un usuario a tu plan, o aceptan una invitación de dispositivo para cada aplicación. La política de tu tailnet debe permitirles acceder. |
| Invitados y visitantes públicos | Un navegador. Tu tailnet debe permitir Funnel, que Tailscale aún considera beta. |

Cuando se emite un certificado HTTPS para una aplicación, su nombre de dispositivo de Tailscale y el nombre DNS de tu tailnet aparecen en un registro público de certificados. Elige nombres de aplicación que no te importe que otros vean.

<a id="installation"></a>
<a id="quickstart"></a>

## Inicio rápido

En macOS o Linux, instala con Homebrew. El binario de macOS está firmado con un certificado Developer ID y notarizado por Apple. Para actualizar más adelante, ejecuta `brew upgrade --cask tslink` y luego otra vez `tslink install` si TSLink se ejecuta como servicio en segundo plano.

```bash
brew install --cask anydoor7/tap/tslink
```

En Windows, descarga `tslink_<version>_windows_<arch>.zip` de la [última versión](https://github.com/anydoor7/tslink/releases/latest), compruébalo con `checksums.txt` y ejecuta `tslink install` para que TSLink arranque al iniciar sesión. El zip no tiene firma Authenticode; [verifica la versión](verify-release.md) con sus sumas de comprobación firmadas y sus atestaciones. Los paquetes `.deb` y `.rpm` para Linux están en la misma página de la versión. Para compilar desde el código fuente, necesitas **Git y Go 1.26.6+**. Los comandos usan bash/zsh; consulta [Configuración de macOS, Linux y Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Necesitas una **cuenta de Tailscale** y [MagicDNS y HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Los dispositivos que acceden de forma privada necesitan Tailscale y permiso de la política de red. TSLink integra Tailscale en el host de las aplicaciones.

Con tu aplicación ya ejecutándose en el puerto 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Usa un nombre libre; si `share` devuelve otro, úsalo en `url`. Completa primero el registro en el navegador y la aprobación del dispositivo indicados; después abre la URL exacta desde un dispositivo autorizado. `share` inicia el servicio en segundo plano cuando hace falta. Este primer acceso privado no necesita un token API de administrador. Comparte archivos con `tslink share ./report.html`; los archivos deben existir y las aplicaciones estar en ejecución. [Configuración completa](getting-started.md)

Cuando te funcione y te resulte útil, puedes [darle una estrella a TSLink](https://github.com/anydoor7/tslink) para que más personas lo descubran. Es totalmente opcional.

<a id="architecture"></a>

## Cómo encaja todo

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC o servidor en la nube: CLI/MCP gestiona un demonio común y nodos por aplicación. Los dispositivos privados usan Tailscale cifrado; HTTPS/Funnel público opcional llega a aplicaciones HTTP mediante control de invitados o publicación abierta explícita." width="960">
</picture>

Piensa en una ruta privada y cifrada hacia tus aplicaciones. **Tailscale aporta el transporte de red y HTTPS; TSLink gestiona el acceso en cada host.** Un demonio ejecuta un nodo integrado por servicio. El portal privado muestra las aplicaciones permitidas; el estado y el historial ayudan a mantenerlas.

El acceso público se activa expresamente: los invitados necesitan el enlace y, si se configura, el PIN; Funnel abierto es accesible para cualquiera con la URL. Ambos usan HTTPS público, no una identidad privada de usuario. TCP sin capa HTTP sigue siendo privado y depende de las políticas del tailnet y la autenticación del backend. TSLink no instala aplicaciones, aísla procesos, crea una VPC en la nube ni agrega varios hosts. Es un proyecto independiente que funciona con Tailscale. [Arquitectura y límites](architecture.md)

<a id="agents"></a>

## Para agentes

Gestiona inventario, estado, URL y acceso mediante CLI/MCP. Empieza por la [guía para agentes](agent-quickstart.md), lee los esquemas actuales de herramientas y comprueba el acceso real antes de informar del éxito.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

La automatización CLI usa `--json`; MCP usa JSON-RPC sobre stdio. [Clientes](mcp-clients.md) · [MCP remoto](remote-mcp.md) · [Roles y ámbitos](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Documentación y licencia

[Todas las guías](INDEX.md) · [Referencia CLI](cli-reference.md) · [IA local](local-ai.md) · [Estado](health-and-alerts.md) · [Historial de acceso](access-log.md) · [Hoja de ruta](roadmap.md)

El inventario multihost está previsto. Se agradecen las [contribuciones](../CONTRIBUTING.md) y los [avisos de seguridad](../SECURITY.md). [Apache 2.0](../LICENSE) permite el uso comercial; conserva [NOTICE](../NOTICE) y los [avisos de terceros](../THIRD_PARTY_NOTICES.md) al redistribuir. La [colaboración comercial](../COMMERCIAL.md) es voluntaria. Las condiciones y planes de Tailscale se aplican por separado.
