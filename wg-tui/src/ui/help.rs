use ratatui::layout::Rect;
use ratatui::style::{Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Paragraph};
use ratatui::Frame;

use crate::theme::DARK_THEME;

pub fn render_help(frame: &mut Frame, area: Rect) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(DARK_THEME.primary))
        .style(Style::default().bg(DARK_THEME.bg))
        .title(Span::styled(" HELP ", Style::default().fg(DARK_THEME.primary).bold()));

    let inner = block.inner(area);
    frame.render_widget(block, area);

    let lines = vec![
        Line::from(""),
        help_line("Tab / ←→", "Switch between tabs"),
        help_line("j / k / ↑↓", "Navigate lists"),
        help_line("/", "Search peers by name/IP"),
        help_line("a", "Approve request"),
        help_line("d", "Delete peer / Deny"),
        help_line("r", "Refresh data"),
        help_line("Ctrl+Arrows", "Move window"),
        help_line("Ctrl+Scroll", "Resize window"),
        help_line("Ctrl+0", "Reset window"),
        help_line("q", "Quit"),
        Line::from(""),
        Line::from(Span::styled("  WG-TUI v0.2.0  ·  Ratatui Dashboard", DARK_THEME.muted)),
    ];

    frame.render_widget(Paragraph::new(lines).style(Style::default().fg(DARK_THEME.text)), inner);
}

fn help_line(key: &str, desc: &str) -> Line<'static> {
    let k = format!("  {:<14}", key);
    let d = desc.to_string();
    Line::from(vec![
        Span::styled(k, DARK_THEME.primary),
        Span::styled(d, DARK_THEME.text),
    ])
}
