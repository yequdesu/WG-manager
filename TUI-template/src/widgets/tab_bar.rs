// ============================================================
// AGENT: REPLACE — Tab enum and labels are domain-specific.
// Replace Tab variants (Dashboard, Peers, ...) with your own tabs.
// Keep: all(), next(), prev(), render_tab_bar() function.
// ============================================================

use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::Tabs;
use ratatui::Frame;

use crate::theme::DARK_THEME;

#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tab {
    Tab1,
    Tab2,
    Tab3,
    Tab4,
}

impl Tab {
    pub fn all() -> &'static [Tab] {
        &[Tab::Tab1, Tab::Tab2, Tab::Tab3, Tab::Tab4]
    }

    pub fn label(&self) -> &'static str {
        match self {
            Tab::Tab1 => "Tab 1",
            Tab::Tab2 => "Tab 2",
            Tab::Tab3 => "Tab 3",
            Tab::Tab4 => "Tab 4",
        }
    }

    pub fn next(self) -> Tab {
        let all = Self::all();
        let idx = all.iter().position(|t| *t == self).unwrap_or(0);
        all[(idx + 1) % all.len()]
    }

    pub fn prev(self) -> Tab {
        let all = Self::all();
        let idx = all.iter().position(|t| *t == self).unwrap_or(0);
        all[(idx + all.len() - 1) % all.len()]
    }
}

pub fn render_tab_bar(frame: &mut Frame, area: Rect, active: Tab) {
    let tabs: Vec<Line> = Tab::all()
        .iter()
        .map(|tab| {
            let label = format!(" {} ", tab.label());
            let style = if *tab == active {
                Style::default()
                    .fg(DARK_THEME.primary)
                    .bg(DARK_THEME.surface)
            } else {
                Style::default().fg(DARK_THEME.muted).bg(DARK_THEME.bg)
            };
            Line::from(Span::styled(label, style))
        })
        .collect();

    let tabs_widget = Tabs::new(tabs)
        .style(Style::default().bg(DARK_THEME.bg))
        .divider("");

    frame.render_widget(tabs_widget, area);
}
