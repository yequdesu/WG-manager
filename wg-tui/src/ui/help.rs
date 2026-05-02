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
        .title(Span::styled(
            " HELP ",
            Style::default().fg(DARK_THEME.primary).bold(),
        ))
        .style(Style::default().bg(DARK_THEME.bg));

    let inner = block.inner(area);
    frame.render_widget(block, area);

    let lines = vec![
        Line::from(""),
        help_line("Tab / ←→", "Switch between tabs"),
        help_line("j / k / ↑↓", "Navigate lists up/down"),
        help_line("Enter", "Select / Confirm"),
        help_line("a", "Approve selected request"),
        help_line("d", "Delete peer / Deny request"),
        help_line("r", "Force refresh all data"),
        help_line("?", "Toggle this help"),
        help_line("q / Esc", "Quit"),
        Line::from(""),
        Line::from(Span::styled(
            "  WG-TUI v0.1.0  —  Ratatui Dashboard",
            DARK_THEME.muted,
        )),
    ];

    let p = Paragraph::new(lines).style(Style::default().fg(DARK_THEME.text));
    frame.render_widget(p, inner);
}

fn help_line(key: &str, desc: &str) -> Line<'static> {
    let k = format!("  {:<12}", key);
    let d = desc.to_string();
    Line::from(vec![
        Span::styled(k, DARK_THEME.primary),
        Span::styled(d, DARK_THEME.text),
    ])
}
