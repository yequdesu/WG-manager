#![allow(dead_code)]

use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::Color;
use ratatui::widgets::Widget;
use rand::Rng;

const PARTICLE_COUNT: usize = 60;
const CONNECTION_DIST: f32 = 22.0;
const REPEL_RADIUS: f32 = 8.0;

struct Particle {
    x: f32,
    y: f32,
    freq_x: f32,
    freq_y: f32,
    phase_x: f32,
    phase_y: f32,
    ch: char,
    color: Color,
    pulse_speed: f32,
    pulse_phase: f32,
}

pub struct ParticleSystem {
    particles: Vec<Particle>,
    rng: rand::rngs::StdRng,
    flash_frames: u16,
}

impl ParticleSystem {
    pub fn new() -> Self {
        use rand::SeedableRng;
        Self {
            particles: Vec::with_capacity(PARTICLE_COUNT),
            rng: rand::rngs::StdRng::from_os_rng(),
            flash_frames: 0,
        }
    }

    pub fn update(&mut self, area: Rect, window_rect: Rect, tick: u64, refresh_just_happened: bool) {
        let w = area.width as f32;
        let h = area.height as f32;

        if refresh_just_happened {
            self.flash_frames = 15;
        }
        if self.flash_frames > 0 {
            self.flash_frames -= 1;
        }

        while self.particles.len() < PARTICLE_COUNT {
            let ch = P_CHARS[self.rng.random_range(0..P_CHARS.len())];
            let hue: u8 = self.rng.random_range(0..6);
            self.particles.push(Particle {
                x: self.rng.random_range(0.0..w),
                y: self.rng.random_range(0.0..h),
                freq_x: self.rng.random_range(0.003..0.012),
                freq_y: self.rng.random_range(0.003..0.012),
                phase_x: self.rng.random_range(0.0..std::f32::consts::TAU),
                phase_y: self.rng.random_range(0.0..std::f32::consts::TAU),
                ch,
                color: hue_color(hue),
                pulse_speed: self.rng.random_range(0.03..0.08),
                pulse_phase: self.rng.random_range(0.0..std::f32::consts::TAU),
            });
        }

        let win_left = window_rect.x as f32;
        let win_right = (window_rect.x + window_rect.width) as f32;
        let win_top = window_rect.y as f32;
        let win_bot = (window_rect.y + window_rect.height) as f32;

        for p in &mut self.particles {
            let dx = (tick as f32 * p.freq_x + p.phase_x).sin() * 0.35;
            let dy = (tick as f32 * p.freq_y + p.phase_y).cos() * 0.30;
            p.x += dx;
            p.y += dy;

            // Window repulsion
            let dist_x = if p.x < win_left {
                win_left - p.x
            } else if p.x > win_right {
                p.x - win_right
            } else {
                0.0
            };
            let dist_y = if p.y < win_top {
                win_top - p.y
            } else if p.y > win_bot {
                p.y - win_bot
            } else {
                0.0
            };
            let dist = (dist_x * dist_x + dist_y * dist_y).sqrt();
            if dist < REPEL_RADIUS && dist > 0.001 {
                let force = (1.0 - dist / REPEL_RADIUS).powi(2) * 0.8;
                let angle = dist_y.atan2(dist_x);
                p.x += angle.cos() * force;
                p.y += angle.sin() * force;
            }

            // Wrap
            if p.x < -2.0 { p.x = w + 2.0; }
            if p.x > w + 2.0 { p.x = -2.0; }
            if p.y < -2.0 { p.y = h + 2.0; }
            if p.y > h + 2.0 { p.y = -2.0; }
        }
    }

    fn particle_alpha(&self, idx: usize, tick: u64) -> f32 {
        let p = &self.particles[idx];
        let base = 0.12 + 0.10 * (tick as f32 * p.pulse_speed + p.pulse_phase).sin();
        let flash = if self.flash_frames > 0 {
            self.flash_frames as f32 / 15.0 * 0.25
        } else {
            0.0
        };
        (base + flash).clamp(0.04, 0.45)
    }
}

const P_CHARS: &[char] = &['●', '○', '◎', '◉', '✦', '·', '·', '·'];

fn hue_color(hue: u8) -> Color {
    match hue {
        0 => Color::Rgb(88, 166, 255),   // primary blue
        1 => Color::Rgb(63, 185, 80),    // accent green
        2 => Color::Rgb(210, 153, 34),   // warning amber
        3 => Color::Rgb(130, 180, 255),  // light blue
        4 => Color::Rgb(100, 200, 120),  // light green
        _ => Color::Rgb(200, 160, 60),   // light amber
    }
}

impl Widget for &ParticleSystem {
    fn render(self, area: Rect, buf: &mut Buffer) {
        // Connection lines first (behind particles)
        for i in 0..self.particles.len() {
            for j in (i + 1)..self.particles.len() {
                let a = &self.particles[i];
                let b = &self.particles[j];
                let dx = a.x - b.x;
                let dy = a.y - b.y;
                let dist = (dx * dx + dy * dy).sqrt();
                if dist < CONNECTION_DIST && dist > 0.5 {
                    let alpha = (1.0 - dist / CONNECTION_DIST) * 0.10;
                    let conn_ch = connector_char(dx, dy);
                    let mid_x = ((a.x + b.x) / 2.0) as u16;
                    let mid_y = ((a.y + b.y) / 2.0) as u16;
                    if mid_x < area.width && mid_y < area.height {
                        if let Some(cell) = buf.cell_mut((area.x + mid_x, area.y + mid_y)) {
                            cell.set_char(conn_ch);
                            cell.set_fg(dim(Color::Rgb(60, 80, 120), alpha));
                        }
                    }
                }
            }
        }

        // Particles on top
        for (i, p) in self.particles.iter().enumerate() {
            let x = p.x as u16;
            let y = p.y as u16;
            if x >= area.width || y >= area.height {
                continue;
            }
            let alpha = self.particle_alpha(i, 0);
            let color = dim(p.color, alpha);
            if let Some(cell) = buf.cell_mut((area.x + x, area.y + y)) {
                cell.set_char(p.ch);
                cell.set_fg(color);
            }
        }
    }
}

fn connector_char(dx: f32, dy: f32) -> char {
    let angle = dy.atan2(dx);
    let pi8 = std::f32::consts::PI / 8.0;
    if angle > -pi8 && angle <= pi8 { '─' }
    else if angle > pi8 && angle <= 3.0 * pi8 { '╲' }
    else if angle > 3.0 * pi8 && angle <= 5.0 * pi8 { '│' }
    else if angle > 5.0 * pi8 && angle <= 7.0 * pi8 { '╱' }
    else if angle > 7.0 * pi8 || angle <= -7.0 * pi8 { '─' }
    else if angle > -7.0 * pi8 && angle <= -5.0 * pi8 { '╲' }
    else if angle > -5.0 * pi8 && angle <= -3.0 * pi8 { '│' }
    else { '╱' }
}

fn dim(c: Color, alpha: f32) -> Color {
    match c {
        Color::Rgb(r, g, b) => Color::Rgb(
            (r as f32 * alpha) as u8,
            (g as f32 * alpha) as u8,
            (b as f32 * alpha) as u8,
        ),
        _ => c,
    }
}
