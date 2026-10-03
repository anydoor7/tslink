<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Accede a tus aplicaciones y gestiónalas desde cualquier lugar.<br>Mantenlas privadas o compártelas a tu manera.</strong></p>

Tus aplicaciones, en tu ordenador o servidor en la nube: accede mediante una red privada cifrada o elige enlaces para invitados en el navegador o acceso público. Gestiónalas tú o mediante un agente.

<p align="center"><a href="#quickstart">Inicio rápido</a> · <a href="#agents">Para agentes</a> · <a href="#documentation">Documentación</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <strong>Español</strong> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Tus aplicaciones, a tu alcance

| Lo que necesitas | Lo que ofrece TSLink |
|---|---|
| Usar tus aplicaciones desde varios dispositivos | Direcciones privadas para paneles domésticos, páginas web solo locales, archivos, API de modelos y servicios TCP en PC o servidor. |
| Compartir con personas concretas | Aplicaciones HTTP/archivos seleccionadas, identidad Tailscale verificada, caducidad y revocación. Los destinatarios necesitan Tailscale. [Personas](people.md) |
| Permitir visitas desde el navegador | Enlaces temporales con PIN opcional para aplicaciones proxy HTTP, o HTTPS expresamente público con Funnel. Los enlaces se pueden reenviar y no verifican la identidad. [Invitados](guest-links.md) |
| Mantener un conjunto de aplicaciones | Inventario por host, portal privado, comprobaciones de estado y alertas, historial de acceso y gestión CLI/MCP con roles de agente, ámbitos por aplicación y registros de auditoría. [Portal](portal.md) · [Permisos MCP](mcp-scopes.md) |

Las [recetas de aplicaciones](apps.md), los [límites de subida](sharing.md), las [duraciones flexibles](durations.md) y el [acceso con QR y las solicitudes](requests.md) facilitan el mantenimiento. Estas funciones están incluidas en este código fuente.

<a id="installation"></a>
<a id="quickstart"></a>

## Inicio rápido

Instala desde el código fuente con **Git y Go 1.26.6+**; aún no se han publicado versiones precompiladas ni Homebrew. Los comandos usan bash/zsh. [Configuración de macOS, Linux y Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Necesitas acceso al repositorio, una **cuenta de Tailscale** y [MagicDNS y HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Los dispositivos que acceden de forma privada necesitan Tailscale y permiso de la política de red. TSLink integra Tailscale en el host de las aplicaciones.

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
