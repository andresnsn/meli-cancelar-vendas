# Meli Cancelar Vendas

Ferramenta de linha de comando em Go para cancelamento em lote de vendas no Mercado Livre.

## Como funciona

1. Abre uma instância do Chrome com um perfil dedicado (mantém o login entre execuções).
2. Você cola as URLs das vendas no terminal (uma por linha).
3. O programa navega automaticamente para cada venda e executa o fluxo de cancelamento:
   - Abre o menu flutuante (⋮)
   - Clica em "Cancelar venda"
   - Seleciona o motivo: "Tive problemas com o envio" → "Outro"
   - Confirma o cancelamento

## Pré-requisitos

- **Google Chrome** instalado na máquina
- Sistema operacional: Windows, macOS ou Linux

## Instalação

### Opção 1: Baixar o binário

Baixe o binário pré-compilado da [página de releases](https://github.com/andresnsn/meli-cancelar-vendas/releases).

### Opção 2: Compilar do fonte

```bash
# Requer Go 1.26+
git clone https://github.com/andresnsn/meli-cancelar-vendas.git
cd meli-cancelar-vendas
go build -o meli-cancelar-vendas .
```

#### Compilar para Windows (cross-compile)

```bash
# 64 bits / Intel-AMD (recomendado para a maioria dos PCs)
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o CancelarVendasML.exe .

# ARM64 (Copilot+ PC / Surface com Snapdragon)
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o CancelarVendasML-arm64.exe .

# 32 bits (fallback para máquinas antigas)
GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w" -o CancelarVendasML-32bits.exe .
```

#### Metadados do executável Windows (publisher/versão + manifest)

O build para Windows embute metadados de versão/fabricante e um manifest de
compatibilidade (`versioninfo.json` + `app.manifest`). Isso ajuda a reduzir
avisos de *reputação* do SmartScreen/Defender ("editor desconhecido"), mas
**não substitui uma assinatura digital**: o binário continua SEM assinatura
Authenticode. Os arquivos `resource_windows_*.syso` (amd64, arm64 e 386) são
gerados a partir do `versioninfo.json`:

```bash
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
go generate ./...
```

> Não deixe um `resource.syso` sem sufixo de arquitetura no diretório — ele
> quebra o build 32 bits (`unknown relocation type 3`).

#### Erro "Esse aplicativo não pode ser executado no seu PC"

Duas causas comuns, com tratamentos diferentes:

1. **Arquitetura incompatível** (mais frequente): o `.exe` foi compilado para uma
   CPU diferente da máquina. Use a variante correspondente ao seu Windows —
   `amd64` (Intel/AMD), `arm64` (Copilot+ PC / Snapdragon) ou `386` (32 bits).
   Metadados/manifest **não** corrigem esse caso.
2. **Política corporativa / reputação** (AppLocker, WDAC, SmartScreen): o
   ambiente bloqueia executáveis sem assinatura. O fim definitivo do aviso exige
   um **certificado de code-signing (Authenticode/EV)**.

#### Compilar para macOS

```bash
GOOS=darwin GOARCH=amd64 go build -o meli-cancelar-vendas-mac .
# Para Apple Silicon (M1/M2/M3):
GOOS=darwin GOARCH=arm64 go build -o meli-cancelar-vendas-mac-arm64 .
```

## Uso

```bash
./meli-cancelar-vendas
```

### Primeiro uso

Na primeira execução, o Chrome abrirá a página de login do Mercado Livre. Faça login normalmente na janela do Chrome. O login será salvo para execuções futuras.

### Cancelando vendas

1. Execute o programa.
2. Cole as URLs das vendas (uma por linha). Exemplo:

```
https://www.mercadolivre.com.br/vendas/omni/lista?filters=&subFilters=&search=2000016547180246
https://www.mercadolivre.com.br/vendas/omni/lista?filters=&subFilters=&search=2000016547180247
https://www.mercadolivre.com.br/vendas/omni/lista?filters=&subFilters=&search=2000016547180248
```

3. Pressione Enter em uma linha vazia para iniciar o processamento.
4. O programa cancelará cada venda sequencialmente, mostrando o progresso no terminal.
5. Ao final, cole mais URLs ou pressione Enter sem nada para sair.

## Formato da URL

```
https://www.mercadolivre.com.br/vendas/omni/lista?filters=&subFilters=&search=NUMERO_DA_VENDA
```

O número após `search=` é o identificador único de cada venda.

## Dados do perfil

O perfil do Chrome é salvo em:

| SO      | Localização                                          |
|---------|------------------------------------------------------|
| Linux   | `~/.meli-cancelar-vendas/`                           |
| macOS   | `~/Library/Application Support/.meli-cancelar-vendas/` |
| Windows | `%LOCALAPPDATA%\.meli-cancelar-vendas\`              |

Para forçar um novo login, delete a pasta do perfil.

## Estrutura do projeto

```
.
├── main.go                     # Código fonte principal
├── versioninfo.json            # Metadados do executável Windows
├── app.manifest                # Manifest de compatibilidade Windows
├── resource_windows_*.syso     # Recursos gerados (amd64/arm64/386)
├── go.mod                      # Dependências Go
├── go.sum                      # Checksums das dependências
├── README.md                   # Este arquivo
└── .gitignore                  # Arquivos ignorados pelo git
```

## Tecnologia

- **[chromedp](https://github.com/chromedp/chromedp)** — Automação do Chrome via Chrome DevTools Protocol (CDP). Mesma tecnologia que Puppeteer/Playwright usam por baixo, mas em Go puro. Gera binário standalone sem dependências externas.
