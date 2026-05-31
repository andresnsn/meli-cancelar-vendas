package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	profileDirName  = ".meli-cancelar-vendas"
	loginCheckURL   = "https://www.mercadolivre.com.br/vendas/omni/lista"
	loginTimeoutMin = 5
)

func main() {
	log.SetFlags(log.Ltime)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║     Meli Cancelar Vendas - Mercado Livre        ║")
	fmt.Println("║     Cancelamento em lote de vendas              ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	profileDir := getProfileDir()
	fmt.Printf("[INFO] Perfil do Chrome: %s\n", profileDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n[INFO] Encerrando...")
		cancel()
	}()

	// Find Chrome binary
	chromePath := findChrome()
	if chromePath != "" {
		fmt.Printf("[INFO] Chrome encontrado: %s\n", chromePath)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-extensions", false),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1280, 900),
	)
	if chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(log.Printf),
	)
	defer browserCancel()

	fmt.Println("[INFO] Iniciando o Chrome...")
	if err := ensureLogin(browserCtx); err != nil {
		log.Fatalf("[ERRO] Falha ao verificar login: %v", err)
	}

	for {
		urls := readURLs()
		if len(urls) == 0 {
			fmt.Println("[INFO] Nenhuma URL fornecida. Encerrando.")
			break
		}

		fmt.Printf("\n[INFO] %d URL(s) para processar.\n\n", len(urls))

		successCount := 0
		failCount := 0

		for i, u := range urls {
			select {
			case <-ctx.Done():
				fmt.Println("[INFO] Cancelado pelo usuário.")
				return
			default:
			}

			fmt.Printf("━━━ [%d/%d] Processando: %s ━━━\n", i+1, len(urls), u)
			if err := cancelSale(browserCtx, u); err != nil {
				fmt.Printf("  [ERRO] Falha: %v\n", err)
				failCount++
			} else {
				fmt.Println("  [OK] Venda cancelada com sucesso!")
				successCount++
			}
			fmt.Println()

			if i < len(urls)-1 {
				time.Sleep(2 * time.Second)
			}
		}

		fmt.Println("═══════════════════════════════════════")
		fmt.Printf("Resultado: %d sucesso(s), %d falha(s)\n", successCount, failCount)
		fmt.Println("═══════════════════════════════════════")
		fmt.Println()
		fmt.Println("Cole mais URLs para continuar ou pressione Enter sem nada para sair.")
	}

	fmt.Println("[INFO] Programa encerrado.")
}

func getProfileDir() string {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.Getenv("USERPROFILE")
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "Library", "Application Support")
	default:
		home, _ := os.UserHomeDir()
		base = home
	}
	return filepath.Join(base, profileDirName)
}

func readURLs() []string {
	fmt.Println("Cole as URLs abaixo (uma por linha).")
	fmt.Println("Quando terminar, pressione Enter em uma linha vazia para iniciar.")
	fmt.Println("Para sair, pressione Enter sem colar nenhuma URL.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var urls []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		if isValidMLURL(line) {
			urls = append(urls, line)
			fmt.Printf("  + URL adicionada (%d)\n", len(urls))
		} else {
			fmt.Printf("  ! URL ignorada (formato inválido): %s\n", line)
		}
	}

	return urls
}

func isValidMLURL(u string) bool {
	return strings.Contains(u, "mercadolivre.com.br/vendas") ||
		strings.Contains(u, "mercadolibre.com") ||
		strings.Contains(u, "mercadolivre.com.br")
}

func ensureLogin(ctx context.Context) error {
	fmt.Println("[INFO] Verificando login no Mercado Livre...")

	if err := chromedp.Run(ctx,
		chromedp.Navigate(loginCheckURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navegar para ML: %w", err)
	}

	time.Sleep(3 * time.Second)

	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return fmt.Errorf("obter URL atual: %w", err)
	}

	if strings.Contains(currentURL, "login") || strings.Contains(currentURL, "registration") {
		fmt.Println()
		fmt.Println("╔══════════════════════════════════════════════════╗")
		fmt.Println("║  LOGIN NECESSÁRIO                                ║")
		fmt.Println("║                                                  ║")
		fmt.Println("║  O Chrome abriu a página de login do ML.         ║")
		fmt.Println("║  Faça login na janela do Chrome que abriu.       ║")
		fmt.Printf("║  Aguardando até %d minutos...                    ║\n", loginTimeoutMin)
		fmt.Println("╚══════════════════════════════════════════════════╝")
		fmt.Println()

		timeout := time.After(time.Duration(loginTimeoutMin) * time.Minute)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				return fmt.Errorf("timeout: login não realizado em %d minutos", loginTimeoutMin)
			case <-ticker.C:
				if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
					continue
				}
				if !strings.Contains(currentURL, "login") && !strings.Contains(currentURL, "registration") {
					fmt.Println("[OK] Login detectado com sucesso!")
					time.Sleep(2 * time.Second)
					return nil
				}
			}
		}
	}

	fmt.Println("[OK] Já está logado no Mercado Livre!")
	return nil
}

// jsClick clicks an element via JavaScript to bypass styled/hidden inputs.
func jsClick(sel string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(
		`document.querySelector(%q)?.click()`, sel,
	), nil)
}

// jsClickXPath clicks the first element matching an XPath via JavaScript.
func jsClickXPath(xpath string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(
		`(function(){
			var r = document.evaluate(%q, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
			if (r.singleNodeValue) { r.singleNodeValue.click(); return true; }
			return false;
		})()`, xpath,
	), nil)
}

func cancelSale(ctx context.Context, saleURL string) error {
	// Step 1: Navigate
	fmt.Println("  [1/6] Navegando para a página da venda...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate(saleURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navegar: %w", err)
	}

	// Step 2: Wait for sale card
	fmt.Println("  [2/6] Aguardando card da venda carregar...")
	cardCtx, cardCancel := context.WithTimeout(ctx, 15*time.Second)
	defer cardCancel()
	if err := chromedp.Run(cardCtx,
		chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("card da venda não encontrado (timeout 15s): %w", err)
	}
	time.Sleep(1 * time.Second)

	// Step 3: Click overflow menu (three dots)
	fmt.Println("  [3/6] Abrindo menu flutuante...")
	if err := chromedp.Run(ctx,
		chromedp.Click(`button[data-testid="open-floating-menu-without-tooltip"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão menu flutuante não encontrado: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)

	// Step 4: Click "Cancelar venda" in dropdown
	fmt.Println("  [4/6] Clicando em 'Cancelar venda'...")
	cancelBtnCtx, cancelBtnCancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancelBtnCancel()
	if err := chromedp.Run(cancelBtnCtx,
		chromedp.WaitVisible(`button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão 'Cancelar venda' não encontrado: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Step 5: Select reason in modal
	fmt.Println("  [5/6] Selecionando motivo do cancelamento...")
	modalCtx, modalCancel := context.WithTimeout(ctx, 10*time.Second)
	defer modalCancel()
	if err := chromedp.Run(modalCtx,
		chromedp.WaitVisible(`div[role="dialog"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("modal não apareceu: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click "Tive problemas com o envio" — use JS click because the <input> is
	// visually hidden behind a styled <div class="andes-radio">.
	// First try clicking the label, then the input directly via JS.
	if err := chromedp.Run(ctx,
		jsClickXPath(`//label[contains(., "problemas com o envio")]`),
	); err != nil {
		if err2 := chromedp.Run(ctx, jsClick(`input[value="shipment_problem"]`)); err2 != nil {
			return fmt.Errorf("radio 'problemas com envio': %w", err2)
		}
	}
	time.Sleep(2 * time.Second)

	// Click "Outro" sub-option (appears after selecting the main reason).
	// Try multiple selectors to be resilient.
	outroClicked := false
	strategies := []struct {
		name   string
		action chromedp.Action
	}{
		{"label xpath", jsClickXPath(`//label[contains(., "Outro")]`)},
		{"span xpath", jsClickXPath(`//span[contains(text(), "Outro")]`)},
		{"input value", jsClick(`input[value="other"]`)},
		{"li text", jsClickXPath(`//li[contains(., "Outro")]`)},
	}
	for _, s := range strategies {
		if err := chromedp.Run(ctx, s.action); err == nil {
			outroClicked = true
			break
		}
	}
	if !outroClicked {
		return fmt.Errorf("sub-opção 'Outro' não encontrada após todas as tentativas")
	}
	time.Sleep(1 * time.Second)

	// Step 6: Confirm cancellation
	fmt.Println("  [6/6] Confirmando cancelamento...")

	// Wait for the confirm button to become enabled (it starts disabled)
	confirmSel := `div[role="dialog"] .sc-cancel-sale__actions button[aria-label="Cancelar venda"]`
	confirmCtx, confirmCancel := context.WithTimeout(ctx, 10*time.Second)
	defer confirmCancel()

	if err := chromedp.Run(confirmCtx,
		chromedp.WaitEnabled(confirmSel, chromedp.ByQuery),
	); err != nil {
		// Fallback: try any loud/primary button inside modal actions
		confirmSel = `div[role="dialog"] .sc-cancel-sale__actions button.andes-button--loud`
		if err2 := chromedp.Run(confirmCtx,
			chromedp.WaitEnabled(confirmSel, chromedp.ByQuery),
		); err2 != nil {
			return fmt.Errorf("botão confirmar não habilitou: %w", err)
		}
	}

	if err := chromedp.Run(ctx,
		chromedp.Click(confirmSel, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("falha ao clicar confirmar: %w", err)
	}

	// Wait for cancellation to process
	time.Sleep(4 * time.Second)

	// Verify: check if modal closed or shows success
	var dialogExists bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('div[role="dialog"]') !== null`, &dialogExists),
	); err != nil {
		log.Printf("  [AVISO] Não foi possível verificar resultado: %v", err)
	}

	if dialogExists {
		var modalText string
		_ = chromedp.Run(ctx,
			chromedp.Text(`div[role="dialog"]`, &modalText, chromedp.ByQuery),
		)
		if strings.Contains(strings.ToLower(modalText), "erro") || strings.Contains(strings.ToLower(modalText), "error") {
			return fmt.Errorf("possível erro no cancelamento: %s", truncate(modalText, 200))
		}
		log.Println("  [AVISO] Modal ainda aberto. Pode ser confirmação — verifique o navegador se necessário.")
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// findChrome searches for a Chrome/Chromium binary in common locations.
func findChrome() string {
	candidates := []string{
		// Standard Linux install paths (absolute, checked first)
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		// macOS
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		// Fallback non-standard paths
		"/opt/.devin/chrome/chrome/linux-133.0.6943.126/chrome-linux64/chrome",
		"/opt/.devin/chrome/chrome/linux-137.0.7118.2/chrome-linux64/chrome",
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
