# Playwright MCP Integration

## MCP Tools Reference

### Navigation

| Tool | Description |
|------|-------------|
| `browser_navigate` | Open a URL |
| `browser_go_back` | Previous page |
| `browser_go_forward` | Next page |
| `browser_reload` | Reload |

### Interaction

| Tool | Description |
|------|-------------|
| `browser_click` | Click element |
| `browser_type` | Type text |
| `browser_fill` | Fill a field |
| `browser_select_option` | Select option |
| `browser_hover` | Hover element |
| `browser_press_key` | Press key |

### Capture

| Tool | Description |
|------|-------------|
| `browser_snapshot` | Accessibility tree |
| `browser_screenshot` | Screenshot |
| `browser_pdf_save` | Generate PDF |

### Testing

| Tool | Description |
|------|-------------|
| `browser_expect` | Assertions |
| `browser_generate_locator` | Generate selector |
| `browser_start_tracing` | Start trace |
| `browser_stop_tracing` | Stop trace |

---

## Playwright MCP Capabilities

- **Navigation** - Open URLs, navigate, screenshots
- **Interaction** - Click, type, select, hover, drag
- **Assertions** - Verify text, elements, states
- **Tracing** - Record sessions for debugging
- **PDF** - Generate PDFs from pages
- **Codegen** - Generate test code

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Skip Phase 1 (Peek/Snapshot) | **FORBIDDEN** | Analyze page before interaction |
| Navigate to malicious sites | **FORBIDDEN** | Security |
| Enter real credentials | **WARNING** | Use fixtures |
| Modify production data | **FORBIDDEN** | Test environment only |

### Legitimate parallelization

| Element | Parallel? | Reason |
|---------|-----------|--------|
| E2E steps (navigate->fill->click) | No - Sequential | Interaction order required |
| Independent final assertions | Yes - Parallel | No dependency between checks |
| Screenshots + validations | Yes - Parallel | Independent operations |
