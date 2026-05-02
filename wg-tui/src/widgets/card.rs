use ratatui::layout::Rect;
use ratatui::style::{Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Paragraph};
use ratatui::Frame;

use crate::theme::DARK_THEME;

pub struct Card<'a> {
    pub title: &'a str,
}

impl<'a> Card<'a> {
    pub fn new(title: &'a str) -> Self {
        Self { title }
    }

    pub fn render(self, frame: &mut Frame, area: Rect, lines: Vec<Line<'a>>) {
        let inner = self.render_block(frame, area);
        let content = Paragraph::new(lines)
            .style(Style::default().fg(DARK_THEME.text).bg(DARK_THEME.surface));
        frame.render_widget(content, inner);
    }

    pub fn render_block(self, frame: &mut Frame, area: Rect) -> Rect {
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(DARK_THEME.border))
            .style(Style::default().bg(DARK_THEME.surface))
            .title(
                Span::styled(
                    format!(" {} ", self.title),
                    Style::default().fg(DARK_THEME.primary).bold(),
                ),
            )
            .title_bottom("");
        let inner = block.inner(area);
        frame.render_widget(block, area);
        inner
    }
}

pub fn status_line(status: &str, label: &str) -> Line<'static> {
    let d = dot_symbol(status);
    let s = status.to_string();
    let l = label.to_string();

    let dot_color = match status {
        "running" | "ok" | "active" | "Active" => DARK_THEME.accent,
        "error" | "Error" => DARK_THEME.danger,
        _ => DARK_THEME.warning,
    };

    Line::from(vec![
        Span::styled(format!("  {}  ", d), Style::default().fg(dot_color)),
        Span::styled(l, Style::default().fg(DARK_THEME.text)),
        Span::styled(format!("  {}", s), Style::default().fg(DARK_THEME.muted)),
    ])
}

fn dot_symbol(status: &str) -> &'static str {
    match status {
        "running" | "ok" | "active" | "Active" => "●",
        _ => "○",
    }
}
