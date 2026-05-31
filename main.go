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
	mlBaseURL       = "https://www.mercadolivre.com.br/vendas/omni/lista"
	profileDirName  = ".ml-cancelar-vendas"
	loginCheckURL   = "https://www.mercadolivre.com.br/vendas/omni/lista"
	loginTimeoutMin = 5
)

func main() {
	log.SetFlags(log.Ltime)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║     ML Cancelar Vendas - Mercado Livre          ║")
	fmt.Println("║     Cancelamento em lote de vendas               ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	profileDir := getProfileDir()
	fmt.Printf("[INFO] Perfil do Chrome: %s\n", profileDir)

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n[INFO] Encerrando...")
		cancel()
	}()

	// Create Chrome with persistent profile
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.UserDataDir(profileDir),
			chromedp.Flag("headless", false),
			chromedp.Flag("disable-gpu", false),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("no-default-browser-check", true),
			chromedp.Flag("disable-extensions", false),
			chromedp.WindowSize(1280, 900),
		)...,
	)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(log.Printf),
	)
	defer browserCancel()

	// Start browser and check login
	fmt.Println("[INFO] Iniciando o Chrome...")
	if err := ensureLogin(browserCtx); err != nil {
		log.Fatalf("[ERRO] Falha ao verificar login: %v", err)
	}

	// Main loop
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

			// Small delay between sales to avoid rate limiting
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

// getProfileDir returns the path to the Chrome profile directory.
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
	default: // linux
		home, _ := os.UserHomeDir()
		base = home
	}
	return filepath.Join(base, profileDirName)
}

// readURLs reads URLs from stdin until an empty line or EOF.
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
		// Validate URL
		if isValidMLURL(line) {
			urls = append(urls, line)
			fmt.Printf("  + URL adicionada (%d)\n", len(urls))
		} else {
			fmt.Printf("  ! URL ignorada (formato inválido): %s\n", line)
		}
	}

	return urls
}

// isValidMLURL checks if the URL matches the expected Mercado Livre pattern.
func isValidMLURL(u string) bool {
	return strings.Contains(u, "mercadolivre.com.br/vendas") ||
		strings.Contains(u, "mercadolibre.com") ||
		strings.Contains(u, "mercadolivre.com.br")
}

// ensureLogin navigates to Mercado Livre and waits for the user to be logged in.
func ensureLogin(ctx context.Context) error {
	fmt.Println("[INFO] Verificando login no Mercado Livre...")

	if err := chromedp.Run(ctx,
		chromedp.Navigate(loginCheckURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navegar para ML: %w", err)
	}

	// Wait a bit for any redirects
	time.Sleep(3 * time.Second)

	// Check if we're on the login page
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

		// Wait for user to complete login (up to loginTimeoutMin minutes)
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

// cancelSale performs the full cancellation flow for a single sale URL.
func cancelSale(ctx context.Context, saleURL string) error {
	fmt.Println("  [1/6] Navegando para a página da venda...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate(saleURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navegar: %w", err)
	}

	// Wait for the sale card to load
	fmt.Println("  [2/6] Aguardando card da venda carregar...")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("card da venda não encontrado: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click the overflow menu button (three dots)
	fmt.Println("  [3/6] Abrindo menu flutuante...")
	if err := chromedp.Run(ctx,
		chromedp.Click(`button[data-testid="open-floating-menu-without-tooltip"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão menu flutuante não encontrado: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click "Cancelar venda" button in the floating menu
	fmt.Println("  [4/6] Clicando em 'Cancelar venda'...")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão 'Cancelar venda' não encontrado: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Wait for the modal dialog to appear
	fmt.Println("  [5/6] Selecionando motivo do cancelamento...")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`div[role="dialog"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("modal não apareceu: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click "Tive problemas com o envio" radio button
	if err := chromedp.Run(ctx,
		chromedp.Click(`input[value="shipment_problem"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("radio 'problemas com envio' não encontrado: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Now look for and click "Outro" sub-option
	// The sub-options appear as a nested list after selecting the main reason
	if err := chromedp.Run(ctx,
		chromedp.Click(`//label[contains(., "Outro")]`, chromedp.BySearch),
	); err != nil {
		// Fallback: try clicking by radio value containing "other"
		if err2 := chromedp.Run(ctx,
			chromedp.Click(`//span[contains(text(), "Outro")]`, chromedp.BySearch),
		); err2 != nil {
			return fmt.Errorf("sub-opção 'Outro' não encontrada: %w (tentativa 2: %v)", err, err2)
		}
	}
	time.Sleep(1 * time.Second)

	// Click the confirm "Cancelar venda" button inside the modal
	fmt.Println("  [6/6] Confirmando cancelamento...")
	// The confirm button is the one inside the modal actions area
	if err := chromedp.Run(ctx,
		chromedp.WaitEnabled(`div[role="dialog"] .sc-cancel-sale__actions button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
		chromedp.Click(`div[role="dialog"] .sc-cancel-sale__actions button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão confirmar cancelamento: %w", err)
	}

	// Wait for the cancellation to be processed
	time.Sleep(3 * time.Second)

	// Check if cancellation was successful by looking for success indicators
	// or checking if the modal closed
	var dialogExists bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('div[role="dialog"]') !== null`, &dialogExists),
	); err != nil {
		log.Printf("  [AVISO] Não foi possível verificar resultado: %v", err)
	}

	if dialogExists {
		// Check if there's an error message or if it's a success dialog
		var modalText string
		chromedp.Run(ctx,
			chromedp.Text(`div[role="dialog"]`, &modalText, chromedp.ByQuery),
		)
		if strings.Contains(strings.ToLower(modalText), "erro") || strings.Contains(strings.ToLower(modalText), "error") {
			return fmt.Errorf("possível erro no cancelamento. Texto do modal: %s", truncate(modalText, 200))
		}
		// Modal still open but no error — might be a confirmation dialog
		log.Println("  [AVISO] Modal ainda aberto após confirmar. Verifique o navegador.")
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
