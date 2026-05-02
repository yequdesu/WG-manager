#![allow(dead_code)]

use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::Color;
use ratatui::widgets::Widget;
use rand::Rng;

const PARTICLE_COUNT: usize = 40;
const PARTICLE_CHARS: &[char] = &['·', '•', '◦', '◌', '∘', '⋅', '⋆', '✦'];

pub struct Particle {
    x: f32,
    y: f32,
    vx: f32,
    vy: f32,
    life: u16,
    max_life: u16,
    ch: char,
    color: Color,
}

pub struct ParticleSystem {
    particles: Vec<Particle>,
    rng: rand::rngs::StdRng,
}

impl ParticleSystem {
    pub fn new() -> Self {
        use rand::SeedableRng;
        Self {
            particles: Vec::with_capacity(PARTICLE_COUNT),
            rng: rand::rngs::StdRng::from_os_rng(),
        }
    }

    pub fn update(&mut self, area: Rect, tick: u64) {
        let w = area.width as f32;
        let h = area.height as f32;

        while self.particles.len() < PARTICLE_COUNT {
            let x = self.rng.random_range(0.0..w);
            let y = self.rng.random_range(0.0..h);
            let speed = self.rng.random_range(0.3..1.5);
            let angle = self.rng.random_range(0.0..std::f32::consts::TAU);
            let life = self.rng.random_range(40..120u16);
            let ch = PARTICLE_CHARS[self.rng.random_range(0..PARTICLE_CHARS.len())];
            self.particles.push(Particle {
                x,
                y,
                vx: angle.cos() * speed * 0.03,
                vy: angle.sin() * speed * 0.03,
                life,
                max_life: life,
                ch,
                color: random_dim_color(&mut self.rng),
            });
        }

        self.particles.retain(|p| p.life > 0);

        let drift_x = (tick as f32 * 0.001).sin() * 0.3;
        let drift_y = (tick as f32 * 0.0013).cos() * 0.2;

        for p in &mut self.particles {
            p.x += p.vx + drift_x * 0.3;
            p.y += p.vy + drift_y * 0.3;
            if p.life > 0 {
                p.life -= 1;
            }

            if p.x < -1.0 { p.x = w + 1.0; }
            if p.x > w + 1.0 { p.x = -1.0; }
            if p.y < -1.0 { p.y = h + 1.0; }
            if p.y > h + 1.0 { p.y = -1.0; }
        }
    }
}

impl Widget for &ParticleSystem {
    fn render(self, area: Rect, buf: &mut Buffer) {
        for p in &self.particles {
            let x = p.x as u16;
            let y = p.y as u16;
            if x >= area.width || y >= area.height {
                continue;
            }
            let alpha = p.life as f32 / p.max_life as f32;
            let color = dim_color(p.color, alpha);
            let cell = buf.cell_mut((area.x + x, area.y + y)).unwrap();
            cell.set_char(p.ch);
            cell.set_fg(color);
        }
    }
}

fn random_dim_color(rng: &mut rand::rngs::StdRng) -> Color {
    let hue: u8 = rng.random_range(0..6);
    let bright: u8 = rng.random_range(40..100);
    match hue {
        0 => Color::Rgb(bright, bright / 3, bright / 3),      // dim red
        1 => Color::Rgb(bright / 3, bright, bright / 3),      // dim green
        2 => Color::Rgb(bright / 3, bright / 3, bright),      // dim blue
        3 => Color::Rgb(bright, bright, bright / 3),           // dim yellow
        4 => Color::Rgb(bright, bright / 3, bright),           // dim magenta
        _ => Color::Rgb(bright / 3, bright, bright),           // dim cyan
    }
}

fn dim_color(c: Color, alpha: f32) -> Color {
    match c {
        Color::Rgb(r, g, b) => Color::Rgb(
            (r as f32 * alpha) as u8,
            (g as f32 * alpha) as u8,
            (b as f32 * alpha) as u8,
        ),
        _ => c,
    }
}
