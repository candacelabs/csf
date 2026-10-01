package kanban

// WDL is parsed by the same Go library available to an embedded host. These
// directives compile the current card ahead of time; they do not start a runtime.
//go:generate go -C ../../.. run ./pkg/widget/internal/cmd/widgetc generate -package card -out services/copilot-adapter/kanban/card services/copilot-adapter/widget-ui/kanban-card.widget
//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate -path card
