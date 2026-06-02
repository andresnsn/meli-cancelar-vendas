package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/xuri/excelize/v2"
)

const (
	profileDirName  = ".meli-cancelar-vendas"
	loginCheckURL   = "https://www.mercadolivre.com.br/vendas/omni/lista"
	loginTimeoutMin = 5
	baseURL         = "https://www.mercadolivre.com.br/vendas/omni/lista?filters=&subFilters=&search="
	noteText        = "Cancelado por erro de integração. renan.ssouza@casasbahia.com.br"
)

// saleResult holds data collected for each sale for the Excel report.
type saleResult struct {
	Index      int
	SaleNumber string
	URL        string
	Status     string
	Reputation string
	Invoice    string
	Cancelled  string
	NoteAdded  string
}

func main() {
	log.SetFlags(log.Ltime)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  MELI - Automação de cancelamento de vendas     ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// Mode selection
	headless := selectMode(reader)

	// Worker count selection
	workers := selectWorkers(reader, headless)

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

	chromePath := findChrome()
	if chromePath != "" {
		fmt.Printf("[INFO] Chrome encontrado: %s\n", chromePath)
	}

	opts := buildAllocOpts(profileDir, headless, chromePath)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(log.Printf),
	)
	defer browserCancel()

	if headless {
		fmt.Println("[INFO] Iniciando o Chrome em modo headless...")
	} else {
		fmt.Println("[INFO] Iniciando o Chrome...")
	}

	if err := ensureLogin(browserCtx); err != nil {
		log.Fatalf("[ERRO] Falha ao verificar login: %v", err)
	}

	// Persistent line reader for paste-friendly input
	lineCh := make(chan string, 1000)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			lineCh <- strings.TrimSpace(line)
			if err != nil {
				close(lineCh)
				return
			}
		}
	}()

	for {
		inputs := readInputs(lineCh)
		if len(inputs) == 0 {
			fmt.Println("[INFO] Nenhuma URL/número fornecido. Encerrando.")
			break
		}

		saleURLs := normalizeInputs(inputs)
		total := len(saleURLs)

		// Avoid opening more workers than necessary
		activeWorkers := workers
		if total < activeWorkers {
			activeWorkers = total
			fmt.Printf("\n[INFO] %d venda(s) para processar — ajustando para %d worker(s).\n\n", total, activeWorkers)
		} else {
			fmt.Printf("\n[INFO] %d venda(s) para processar com %d worker(s).\n\n", total, activeWorkers)
		}

		results := make([]saleResult, total)

		if activeWorkers <= 1 {
			// Sequential processing
			for i, su := range saleURLs {
				select {
				case <-ctx.Done():
					fmt.Println("[INFO] Cancelado pelo usuário.")
					goto done
				default:
				}

				fmt.Printf("━━━ [%d/%d] Processando: %s ━━━\n", i+1, total, su.number)
				results[i] = processSale(browserCtx, su.url, su.number, i+1, total)
				fmt.Println()

				if i < total-1 {
					time.Sleep(2 * time.Second)
				}
			}
		} else {
			// Parallel processing: each worker gets its own independent Chrome process
			// (tabs in a single browser serialize actions on inactive tabs)
			type workerJob struct {
				idx  int
				sale saleURL
			}

			jobs := make(chan workerJob, total)
			for i, su := range saleURLs {
				jobs <- workerJob{idx: i, sale: su}
			}
			close(jobs)

			// Copy profile for each worker so each Chrome instance has the login session
			workerProfiles := make([]string, activeWorkers)
			for w := 0; w < activeWorkers; w++ {
				wp := profileDir + fmt.Sprintf("-worker-%d", w)
				os.RemoveAll(wp)
				if runtime.GOOS == "windows" {
					osexec.Command("xcopy", profileDir, wp, "/E", "/I", "/Q", "/Y").Run()
				} else {
					osexec.Command("cp", "-r", profileDir, wp).Run()
				}
				// Remove lock files so Chrome can start fresh with this profile copy
				os.Remove(filepath.Join(wp, "SingletonLock"))
				os.Remove(filepath.Join(wp, "SingletonSocket"))
				os.Remove(filepath.Join(wp, "SingletonCookie"))
				workerProfiles[w] = wp
			}

			var wg sync.WaitGroup
			for w := 0; w < activeWorkers; w++ {
				wg.Add(1)
				go func(workerID int) {
					defer wg.Done()

					wOpts := buildAllocOpts(workerProfiles[workerID], headless, chromePath)
					wAllocCtx, wAllocCancel := chromedp.NewExecAllocator(ctx, wOpts...)
					defer wAllocCancel()
					wBrowserCtx, wBrowserCancel := chromedp.NewContext(wAllocCtx)
					defer wBrowserCancel()

					// Start browser
					if err := chromedp.Run(wBrowserCtx, chromedp.Navigate("about:blank")); err != nil {
						fmt.Printf("[ERRO] Worker %d: falha ao iniciar browser: %v\n", workerID+1, err)
						return
					}

					for job := range jobs {
						fmt.Printf("━━━ [%d/%d] Processando: %s (worker %d) ━━━\n", job.idx+1, total, job.sale.number, workerID+1)
						results[job.idx] = processSale(wBrowserCtx, job.sale.url, job.sale.number, job.idx+1, total)
						fmt.Println()
					}
				}(w)
			}
			wg.Wait()

			// Clean up worker profiles
			for _, wp := range workerProfiles {
				os.RemoveAll(wp)
			}
		}

	done:
		printSummary(results)

		excelPath := generateExcel(results)
		if excelPath != "" {
			fmt.Printf("[INFO] Relatório salvo em: %s\n", excelPath)
		}

		fmt.Println()
		fmt.Println("Cole mais URLs/números para continuar ou pressione Enter sem nada para sair.")
	}

	fmt.Println("[INFO] Programa encerrado.")
}

// buildAllocOpts constructs Chrome allocator options for a given profile directory.
func buildAllocOpts(profileDir string, headless bool, chromePath string) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-extensions", false),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1280, 900),
	)
	if headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
		opts = append(opts, chromedp.Flag("disable-blink-features", "AutomationControlled"))
		opts = append(opts, chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"))
	} else {
		opts = append(opts, chromedp.Flag("headless", false))
	}
	if chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}
	return opts
}

// selectMode prompts the user to choose between traditional and headless mode.
func selectMode(reader *bufio.Reader) bool {
	fmt.Println("Selecione o modo de execução:")
	fmt.Println("  1 - Chrome visível (mais consumo de memória e CPU)")
	fmt.Println("  2 - Chrome em background (mais performance)")
	fmt.Println()

	for {
		fmt.Print("Opção (1 ou 2): ")
		line, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(line)
		switch choice {
		case "1":
			fmt.Println("[INFO] Modo: Chrome visível")
			fmt.Println()
			return false
		case "2":
			fmt.Println("[INFO] Modo: Chrome em background")
			fmt.Println()
			return true
		default:
			fmt.Println("  Opção inválida. Digite 1 ou 2.")
		}
	}
}

// selectWorkers prompts the user for the number of parallel workers.
func selectWorkers(reader *bufio.Reader, headless bool) int {
	maxSuggested := 3
	if headless {
		maxSuggested = 5
	}
	fmt.Printf("Quantas vendas processar em paralelo? (1 = sequencial, recomendado até %d)\n", maxSuggested)
	for {
		fmt.Print("Workers: ")
		line, _ := reader.ReadString('\n')
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || n < 1 {
			fmt.Println("  Digite um número válido (mínimo 1).")
			continue
		}
		if n > 10 {
			fmt.Println("  Máximo de 10 workers. Ajustando para 10.")
			n = 10
		}
		if n == 1 {
			fmt.Println("[INFO] Modo sequencial (1 worker)")
		} else {
			fmt.Printf("[INFO] Processamento paralelo com %d workers\n", n)
		}
		fmt.Println()
		return n
	}
}

type saleURL struct {
	url    string
	number string
}

// normalizeInputs converts raw user inputs (URLs or sale numbers) to normalized URLs.
func normalizeInputs(inputs []string) []saleURL {
	var result []saleURL
	for _, input := range inputs {
		number, normalized := normalizeInput(input)
		result = append(result, saleURL{url: normalized, number: number})
	}
	return result
}

// normalizeInput handles a single input: bare number, full URL, or URL with extra params.
func normalizeInput(input string) (string, string) {
	if matched, _ := regexp.MatchString(`^\d+$`, input); matched {
		return input, baseURL + input
	}

	if u, err := url.Parse(input); err == nil {
		search := u.Query().Get("search")
		if search != "" {
			return search, baseURL + search
		}
	}

	re := regexp.MustCompile(`search=(\d+)`)
	if m := re.FindStringSubmatch(input); len(m) > 1 {
		return m[1], baseURL + m[1]
	}

	return input, input
}

// readInputs reads URLs or sale numbers from the line channel.
// Auto-detects paste completion: after receiving input, if no more data arrives
// within 2 seconds, it starts processing automatically.
func readInputs(lineCh <-chan string) []string {
	fmt.Println("Cole as URLs ou números de venda abaixo.")
	fmt.Println("Após colar, aguarde 2 segundos para iniciar automaticamente,")
	fmt.Println("ou pressione Enter em uma linha vazia para iniciar.")
	fmt.Println()

	var inputs []string
	for {
		if len(inputs) == 0 {
			// Wait indefinitely for the first line
			line, ok := <-lineCh
			if !ok {
				return inputs
			}
			if line == "" {
				return nil
			}
			inputs = append(inputs, line)
			fmt.Printf("  + Entrada adicionada (%d)\n", len(inputs))
		} else {
			// After first input, use timeout to auto-detect end of paste
			select {
			case line, ok := <-lineCh:
				if !ok || line == "" {
					return inputs
				}
				inputs = append(inputs, line)
				fmt.Printf("  + Entrada adicionada (%d)\n", len(inputs))
			case <-time.After(2 * time.Second):
				fmt.Printf("  [AUTO] %d entrada(s) detectada(s). Iniciando...\n", len(inputs))
				return inputs
			}
		}
	}
}

// processSale handles the full flow for a single sale: collect data, check invoice, cancel, add note.
func processSale(ctx context.Context, saleURL, saleNumber string, current, total int) saleResult {
	prefix := fmt.Sprintf("  [%d/%d]", current, total)

	result := saleResult{
		Index:      current,
		SaleNumber: saleNumber,
		URL:        saleURL,
		Status:     "-",
		Reputation: "-",
		Invoice:    "-",
		Cancelled:  "Não processado",
		NoteAdded:  "Não",
	}

	// Step 1: Navigate
	fmt.Printf("%s [1/8] Navegando para a página da venda...\n", prefix)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(saleURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		fmt.Printf("%s [ERRO] Falha ao navegar: %v\n", prefix, err)
		result.Cancelled = "Erro: falha ao navegar"
		return result
	}

	// Step 2: Wait for card
	fmt.Printf("%s [2/8] Aguardando card da venda carregar...\n", prefix)
	cardCtx, cardCancel := context.WithTimeout(ctx, 20*time.Second)
	defer cardCancel()
	if err := chromedp.Run(cardCtx,
		chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery),
	); err != nil {
		fmt.Printf("%s [ERRO] Card não encontrado: %v\n", prefix, err)
		result.Cancelled = "Erro: card não encontrado"
		return result
	}
	time.Sleep(1 * time.Second)

	// Step 3: Collect sale data
	fmt.Printf("%s [3/8] Coletando dados da venda...\n", prefix)
	collectSaleData(ctx, &result)
	fmt.Printf("%s        Status: %s | Reputação: %s | NF: %s\n", prefix, result.Status, result.Reputation, result.Invoice)

	// Step 4: Check invoice condition
	// Rule: only cancel if there is NO invoice. If invoice was issued, do NOT cancel.
	fmt.Printf("%s [4/8] Verificando condição de nota fiscal...\n", prefix)
	if strings.Contains(result.Invoice, "Nota fiscal emitida") {
		fmt.Printf("%s [SKIP] Nota fiscal emitida — venda NÃO será cancelada.\n", prefix)
		result.Cancelled = "Não cancelado: NF emitida"
		return result
	}
	fmt.Printf("%s        Sem nota fiscal — prosseguindo com cancelamento.\n", prefix)

	// Check if already cancelled
	if strings.Contains(strings.ToLower(result.Status), "cancel") {
		fmt.Printf("%s [SKIP] Venda já está cancelada (status: %s).\n", prefix, result.Status)
		result.Cancelled = "Já cancelada"
		return result
	}

	// Steps 5-6b: Cancel with retry and verification
	maxAttempts := 3
	cancelled := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			fmt.Printf("%s [RETRY] Tentativa %d de cancelamento...\n", prefix, attempt)
			// Reload page before retry
			chromedp.Run(ctx, chromedp.Navigate(saleURL), chromedp.WaitReady("body", chromedp.ByQuery))
			retryCardCtx, retryCardCancel := context.WithTimeout(ctx, 15*time.Second)
			chromedp.Run(retryCardCtx, chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery))
			retryCardCancel()
			time.Sleep(2 * time.Second)
		}

		// Step 5: Open menu and click cancel
		fmt.Printf("%s [5/8] Abrindo menu e clicando 'Cancelar venda'...\n", prefix)
		if err := openMenuAndClickCancel(ctx); err != nil {
			fmt.Printf("%s [ERRO] %v\n", prefix, err)
			if attempt == maxAttempts {
				result.Cancelled = fmt.Sprintf("Erro: %v", err)
				return result
			}
			continue
		}

		// Step 6: Select reason and confirm
		fmt.Printf("%s [6/8] Selecionando motivo e confirmando...\n", prefix)
		if err := selectReasonAndConfirm(ctx); err != nil {
			fmt.Printf("%s [ERRO] %v\n", prefix, err)
			if attempt == maxAttempts {
				result.Cancelled = fmt.Sprintf("Erro: %v", err)
				return result
			}
			continue
		}

		// Step 6b: Verify cancellation via snackbar + mandatory page reload
		// 1) Check for snackbar confirmation (appears within seconds)
		// 2) Always reload page and verify status field changed to contain "cancel"
		fmt.Printf("%s [6b/8] Verificando cancelamento (aguardando confirmação)...\n", prefix)

		snackbarFound := false
		var snackbarText string
		snackCtx, snackCancel := context.WithTimeout(ctx, 10*time.Second)
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			chromedp.Run(snackCtx, chromedp.Evaluate(`
				(function() {
					var el = document.querySelector('.andes-snackbar__message');
					if (el) return el.textContent.trim();
					var sr = document.querySelector('span.andes-visually-hidden[role="alert"]');
					if (sr && sr.textContent.includes('cancelamos')) return sr.textContent.trim();
					return '';
				})()
			`, &snackbarText))
			if strings.Contains(strings.ToLower(snackbarText), "cancelamos") {
				snackbarFound = true
				break
			}
		}
		snackCancel()

		if snackbarFound {
			fmt.Printf("%s        Snackbar detectado: \"%s\"\n", prefix, snackbarText)
		} else {
			fmt.Printf("%s        Snackbar não detectado, verificando via reload...\n", prefix)
		}

		// Mandatory: reload page and confirm status contains "cancel"
		time.Sleep(2 * time.Second)
		chromedp.Run(ctx, chromedp.Navigate(saleURL), chromedp.WaitReady("body", chromedp.ByQuery))
		vCtx, vCancel := context.WithTimeout(ctx, 15*time.Second)
		chromedp.Run(vCtx, chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery))
		vCancel()
		time.Sleep(2 * time.Second)

		var verifyStatus string
		chromedp.Run(ctx, chromedp.Evaluate(`
			(function() {
				var el = document.querySelector('.sc-status-action-row__status');
				if (el) return el.textContent.trim();
				return '';
			})()
		`, &verifyStatus))

		if strings.Contains(strings.ToLower(verifyStatus), "cancel") {
			result.Cancelled = "Sim"
			fmt.Printf("%s [OK] Venda cancelada com sucesso! (status: %s)\n", prefix, verifyStatus)
			cancelled = true
			break
		} else {
			fmt.Printf("%s [AVISO] Tentativa %d: cancelamento NÃO confirmado (status: '%s')\n", prefix, attempt, verifyStatus)
			if attempt == maxAttempts {
				result.Cancelled = fmt.Sprintf("Falhou (status: %s)", verifyStatus)
				fmt.Printf("%s [ERRO] Cancelamento NÃO confirmado após %d tentativas.\n", prefix, maxAttempts)
				return result
			}
		}
	}

	if !cancelled {
		return result
	}

	// Step 7: Add note (page already reloaded from verification)
	fmt.Printf("%s [7/8] Adicionando nota...\n", prefix)
	if err := addNote(ctx, saleURL, prefix); err != nil {
		fmt.Printf("%s [AVISO] Falha ao adicionar nota: %v\n", prefix, err)
		result.NoteAdded = fmt.Sprintf("Erro: %v", err)
	} else {
		result.NoteAdded = "Sim"
		fmt.Printf("%s [OK] Nota adicionada com sucesso!\n", prefix)
	}

	// Step 8: Done
	fmt.Printf("%s [8/8] Concluído!\n", prefix)
	return result
}

// collectSaleData extracts status, reputation, and invoice info from the page.
func collectSaleData(ctx context.Context, result *saleResult) {
	collectCtx, collectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer collectCancel()

	var status string
	if err := chromedp.Run(collectCtx,
		chromedp.Text(`span.sc-status-action-row__status`, &status, chromedp.ByQuery),
	); err == nil && status != "" {
		result.Status = strings.TrimSpace(status)
	}

	var reputation string
	if err := chromedp.Run(collectCtx,
		chromedp.Text(`.left-column__typography-reputation-type`, &reputation, chromedp.ByQuery),
	); err == nil && reputation != "" {
		result.Reputation = strings.TrimSpace(reputation)
	}

	var invoice string
	if err := chromedp.Run(collectCtx,
		chromedp.Evaluate(`
			(function() {
				var el = document.querySelector('.tooltip-invoices .andes-visually-hidden');
				if (el) return el.textContent.trim();
				el = document.querySelector('.tooltip-invoices');
				if (el) return el.textContent.trim();
				return '';
			})()
		`, &invoice),
	); err == nil && invoice != "" {
		result.Invoice = strings.TrimSpace(invoice)
	}
}

// openMenuAndClickCancel opens the overflow menu and clicks "Cancelar venda".
func openMenuAndClickCancel(ctx context.Context) error {
	// Click the menu button (3 dots) with JS fallback
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var el = document.querySelector('button[data-testid="open-floating-menu-without-tooltip"]');
			if (!el) el = document.querySelector('button[aria-label="open-floating-menu"]');
			if (el) { el.click(); return true; }
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("menu flutuante não encontrado: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Click "Cancelar venda" using aria-label, with fallbacks
	cancelBtnCtx, cancelBtnCancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancelBtnCancel()

	if err := chromedp.Run(cancelBtnCtx,
		chromedp.WaitVisible(`button[aria-label="Cancelar venda"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão 'Cancelar venda' não apareceu: %w", err)
	}

	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var el = document.querySelector('button[aria-label="Cancelar venda"]');
			if (el) { el.click(); return true; }
			// Fallback: find by text content
			var btns = document.querySelectorAll('button[id^="secondary-actions-list"]');
			for (var i = 0; i < btns.length; i++) {
				if (btns[i].textContent.includes('Cancelar venda')) {
					btns[i].click();
					return true;
				}
			}
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("botão 'Cancelar venda' não encontrado: %w", err)
	}
	time.Sleep(2 * time.Second)
	return nil
}

// selectReasonAndConfirm selects the cancellation reason and confirms.
func selectReasonAndConfirm(ctx context.Context) error {
	// Wait for the cancel modal specifically (not the chat widget)
	modalCtx, modalCancel := context.WithTimeout(ctx, 15*time.Second)
	defer modalCancel()
	if err := chromedp.Run(modalCtx,
		chromedp.WaitVisible(`.andes-modal.sc-modal-action`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("modal de cancelamento não apareceu: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click "Tive problemas com o envio" inside the cancel modal
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var modal = document.querySelector('.andes-modal.sc-modal-action');
			if (!modal) return false;
			var labels = modal.querySelectorAll('label');
			for (var i = 0; i < labels.length; i++) {
				if (labels[i].textContent.includes('problemas com o envio')) {
					labels[i].click();
					return true;
				}
			}
			var input = modal.querySelector('input[value="shipment_problem"]');
			if (input) { input.click(); return true; }
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("radio 'problemas com envio': %w", err)
	}
	time.Sleep(2 * time.Second)

	// Click "Outro" sub-option (value is "shipment_problem_other")
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var modal = document.querySelector('.andes-modal.sc-modal-action');
			if (!modal) return false;
			var labels = modal.querySelectorAll('label');
			for (var i = 0; i < labels.length; i++) {
				if (labels[i].textContent.trim() === 'Outro') {
					labels[i].click();
					return true;
				}
			}
			var input = modal.querySelector('input[value="shipment_problem_other"]');
			if (input) { input.click(); return true; }
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("sub-opção 'Outro': %w", err)
	}
	time.Sleep(1 * time.Second)

	// Wait for confirm button to become enabled and click it
	confirmSel := `.andes-modal.sc-modal-action .sc-cancel-sale__actions button[aria-label="Cancelar venda"]`
	confirmCtx, confirmCancel := context.WithTimeout(ctx, 15*time.Second)
	defer confirmCancel()

	if err := chromedp.Run(confirmCtx,
		chromedp.WaitEnabled(confirmSel, chromedp.ByQuery),
	); err != nil {
		// Fallback: try the loud button in the modal actions
		confirmSel = `.andes-modal.sc-modal-action .sc-cancel-sale__actions button.andes-button--loud`
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

	time.Sleep(4 * time.Second)
	return nil
}

// addNote reloads the sale page and adds the standard note.
func addNote(ctx context.Context, saleURL, prefix string) error {
	if err := chromedp.Run(ctx,
		chromedp.Navigate(saleURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("falha ao recarregar: %w", err)
	}

	cardCtx, cardCancel := context.WithTimeout(ctx, 20*time.Second)
	defer cardCancel()
	if err := chromedp.Run(cardCtx,
		chromedp.WaitVisible(`.row-card-container`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("card não carregou após reload: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Click menu button via JS
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var el = document.querySelector('button[data-testid="open-floating-menu-without-tooltip"]');
			if (!el) el = document.querySelector('button[aria-label="open-floating-menu"]');
			if (el) { el.click(); return true; }
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("menu flutuante não encontrado: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Click "Adicionar nota" via JS with fallback
	addNoteBtnCtx, addNoteBtnCancel := context.WithTimeout(ctx, 15*time.Second)
	defer addNoteBtnCancel()
	if err := chromedp.Run(addNoteBtnCtx,
		chromedp.WaitVisible(`button[aria-label="Adicionar nota"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("botão 'Adicionar nota' não apareceu: %w", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		(function() {
			var el = document.querySelector('button[aria-label="Adicionar nota"]');
			if (el) { el.click(); return true; }
			var btns = document.querySelectorAll('button[id^="secondary-actions-list"]');
			for (var i = 0; i < btns.length; i++) {
				if (btns[i].textContent.includes('Adicionar nota')) {
					btns[i].click();
					return true;
				}
			}
			return false;
		})()
	`, nil)); err != nil {
		return fmt.Errorf("botão 'Adicionar nota' não encontrado: %w", err)
	}
	time.Sleep(2 * time.Second)

	noteInputCtx, noteInputCancel := context.WithTimeout(ctx, 10*time.Second)
	defer noteInputCancel()
	if err := chromedp.Run(noteInputCtx,
		chromedp.WaitVisible(`input[data-testid="noteInput"]`, chromedp.ByQuery),
		chromedp.Click(`input[data-testid="noteInput"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("campo de nota não encontrado: %w", err)
	}

	// Use React-compatible value setter to trigger save button enable
	jsSetNote := fmt.Sprintf(`
		(function() {
			var input = document.querySelector('input[data-testid="noteInput"]');
			if (!input) return false;
			var setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
			setter.call(input, %q);
			input.dispatchEvent(new Event('input', { bubbles: true }));
			input.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		})()
	`, noteText)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(jsSetNote, &ok)); err != nil || !ok {
		return fmt.Errorf("falha ao preencher nota")
	}
	time.Sleep(1 * time.Second)

	saveBtnCtx, saveBtnCancel := context.WithTimeout(ctx, 10*time.Second)
	defer saveBtnCancel()
	if err := chromedp.Run(saveBtnCtx,
		chromedp.WaitEnabled(`button[aria-label="save-note"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="save-note"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("falha ao salvar nota: %w", err)
	}
	time.Sleep(2 * time.Second)

	return nil
}

// generateExcel creates an Excel report with the results.
func generateExcel(results []saleResult) string {
	if len(results) == 0 {
		return ""
	}

	f := excelize.NewFile()
	sheet := "Relatório"
	idx, _ := f.NewSheet(sheet)
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	headers := []string{"Número da Venda", "URL", "Status", "Reputação", "Nota Fiscal", "Cancelado", "Nota Adicionada"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"4472C4"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	f.SetCellStyle(sheet, "A1", "G1", headerStyle)

	for i, r := range results {
		row := i + 2
		f.SetCellValue(sheet, cellName(1, row), r.SaleNumber)
		f.SetCellValue(sheet, cellName(2, row), r.URL)
		f.SetCellValue(sheet, cellName(3, row), r.Status)
		f.SetCellValue(sheet, cellName(4, row), r.Reputation)
		f.SetCellValue(sheet, cellName(5, row), r.Invoice)
		f.SetCellValue(sheet, cellName(6, row), r.Cancelled)
		f.SetCellValue(sheet, cellName(7, row), r.NoteAdded)
	}

	colWidths := []float64{22, 70, 25, 30, 25, 35, 20}
	for i, w := range colWidths {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, colName, colName, w)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	baseName := fmt.Sprintf("relatorio_cancelamento_%s.xlsx", timestamp)

	// Save in the same directory as the executable
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		fullPath := filepath.Join(exeDir, baseName)
		if err := f.SaveAs(fullPath); err != nil {
			fmt.Printf("[AVISO] Falha ao salvar em %s, tentando diretório atual...\n", fullPath)
			if err2 := f.SaveAs(baseName); err2 != nil {
				fmt.Printf("[ERRO] Falha ao salvar Excel: %v\n", err2)
				return ""
			}
			return baseName
		}
		return fullPath
	}
	if err := f.SaveAs(baseName); err != nil {
		fmt.Printf("[ERRO] Falha ao salvar Excel: %v\n", err)
		return ""
	}
	return baseName
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

func printSummary(results []saleResult) {
	cancelled := 0
	alreadyCancelled := 0
	skipped := 0
	errors := 0
	for _, r := range results {
		switch {
		case r.Cancelled == "Sim":
			cancelled++
		case r.Cancelled == "Já cancelada":
			alreadyCancelled++
		case strings.HasPrefix(r.Cancelled, "Não cancelado"):
			skipped++
		default:
			errors++
		}
	}
	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Printf("Resultado: %d cancelada(s), %d já cancelada(s), %d pulada(s), %d erro(s)\n", cancelled, alreadyCancelled, skipped, errors)
	fmt.Println("═══════════════════════════════════════")
}

// --- Utility functions ---

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


func findChrome() string {
	candidates := []string{
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
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
