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
# Requer Go 1.22+
git clone https://github.com/andresnsn/meli-cancelar-vendas.git
cd meli-cancelar-vendas
go build -o meli-cancelar-vendas .
```

#### Compilar para Windows (cross-compile)

```bash
GOOS=windows GOARCH=amd64 go build -o meli-cancelar-vendas.exe .
```

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
├── main.go          # Código fonte principal
├── go.mod           # Dependências Go
├── go.sum           # Checksums das dependências
├── README.md        # Este arquivo
└── .gitignore       # Arquivos ignorados pelo git
```

## Tecnologia

- **[chromedp](https://github.com/chromedp/chromedp)** — Automação do Chrome via Chrome DevTools Protocol (CDP). Mesma tecnologia que Puppeteer/Playwright usam por baixo, mas em Go puro. Gera binário standalone sem dependências externas.
