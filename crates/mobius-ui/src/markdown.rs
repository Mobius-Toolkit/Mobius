use dioxus::prelude::*;
use pulldown_cmark::{Event, HeadingLevel, Options, Parser, Tag};

enum Node {
    Text(String),
    Code(String),
    Break,
    Rule,
    Checkbox(bool),
    Image { href: Option<String>, alt: String },
    Wrap(Wrap, Vec<Node>),
}

// A container element. `Root` and `Image` only mark a parse frame; they never appear in a Node.
enum Wrap {
    Root,
    Paragraph,
    Heading(HeadingLevel),
    Quote,
    CodeBlock,
    List(Option<u64>),
    Item,
    Emphasis,
    Strong,
    Strikethrough,
    // `None` keeps the link text but drops an unsafe URL like `javascript:`.
    Link(Option<String>),
    Image(Option<String>),
    Table,
    TableHead,
    TableRow,
    Cell,
    Passthrough,
}

struct Frame {
    kind: Wrap,
    children: Vec<Node>,
}

// A link is safe when it has no scheme or when the scheme is http, https, or mailto.
fn safe_href(url: &str) -> Option<String> {
    let url = url.trim();
    let colon = url.find(':');
    // A `/`, `?`, or `#` before the colon makes the URL relative, for example `a/b:c`.
    let relative = url
        .find(['/', '?', '#'])
        .is_some_and(|at| colon.is_none_or(|colon| at < colon));
    if relative || colon.is_none() {
        return Some(url.to_string());
    }
    let scheme = &url[..colon.unwrap()];
    matches!(
        scheme.to_ascii_lowercase().as_str(),
        "http" | "https" | "mailto"
    )
    .then(|| url.to_string())
}

fn kind(tag: &Tag) -> Wrap {
    match tag {
        Tag::Paragraph => Wrap::Paragraph,
        Tag::Heading { level, .. } => Wrap::Heading(*level),
        Tag::BlockQuote(_) => Wrap::Quote,
        Tag::CodeBlock(_) => Wrap::CodeBlock,
        Tag::List(start) => Wrap::List(*start),
        Tag::Item => Wrap::Item,
        Tag::Table(_) => Wrap::Table,
        Tag::TableHead => Wrap::TableHead,
        Tag::TableRow => Wrap::TableRow,
        Tag::TableCell => Wrap::Cell,
        Tag::Emphasis => Wrap::Emphasis,
        Tag::Strong => Wrap::Strong,
        Tag::Strikethrough => Wrap::Strikethrough,
        Tag::Link { dest_url, .. } => Wrap::Link(safe_href(dest_url)),
        Tag::Image { dest_url, .. } => Wrap::Image(safe_href(dest_url)),
        _ => Wrap::Passthrough,
    }
}

fn alt_text(nodes: &[Node]) -> String {
    let mut alt = String::new();
    for node in nodes {
        match node {
            Node::Text(text) | Node::Code(text) => alt.push_str(text),
            Node::Wrap(_, children) => alt.push_str(&alt_text(children)),
            _ => {}
        }
    }
    alt
}

// Raw HTML becomes a Text node, so the renderer escapes it instead of running it.
fn parse(text: &str) -> Vec<Node> {
    let options =
        Options::ENABLE_TABLES | Options::ENABLE_STRIKETHROUGH | Options::ENABLE_TASKLISTS;
    let mut stack = vec![Frame {
        kind: Wrap::Root,
        children: Vec::new(),
    }];
    for event in Parser::new_ext(text, options) {
        match event {
            Event::Start(tag) => stack.push(Frame {
                kind: kind(&tag),
                children: Vec::new(),
            }),
            Event::End(_) if stack.len() > 1 => {
                let frame = stack.pop().unwrap();
                let node = match frame.kind {
                    Wrap::Image(href) => Node::Image {
                        href,
                        alt: alt_text(&frame.children),
                    },
                    kind => Node::Wrap(kind, frame.children),
                };
                stack.last_mut().unwrap().children.push(node);
            }
            Event::Text(text) => stack
                .last_mut()
                .unwrap()
                .children
                .push(Node::Text(text.into_string())),
            Event::Code(code) => stack
                .last_mut()
                .unwrap()
                .children
                .push(Node::Code(code.into_string())),
            Event::Html(html) | Event::InlineHtml(html) => stack
                .last_mut()
                .unwrap()
                .children
                .push(Node::Text(html.into_string())),
            Event::SoftBreak | Event::HardBreak => {
                stack.last_mut().unwrap().children.push(Node::Break)
            }
            Event::Rule => stack.last_mut().unwrap().children.push(Node::Rule),
            Event::TaskListMarker(done) => stack
                .last_mut()
                .unwrap()
                .children
                .push(Node::Checkbox(done)),
            _ => {}
        }
    }
    stack.pop().map(|root| root.children).unwrap_or_default()
}

fn render_nodes(nodes: &[Node]) -> Element {
    rsx! {
        for node in nodes {
            {render_node(node)}
        }
    }
}

fn cells(nodes: &[Node]) -> impl Iterator<Item = &[Node]> + '_ {
    nodes.iter().filter_map(|node| match node {
        Node::Wrap(Wrap::Cell, children) => Some(children.as_slice()),
        _ => None,
    })
}

fn render_cell(children: &[Node], head: bool) -> Element {
    if head {
        rsx! { th { {render_nodes(children)} } }
    } else {
        rsx! { td { {render_nodes(children)} } }
    }
}

fn render_node(node: &Node) -> Element {
    match node {
        Node::Text(text) => rsx! { "{text}" },
        Node::Code(code) => rsx! { code { "{code}" } },
        Node::Break => rsx! { br {} },
        Node::Rule => rsx! { hr {} },
        Node::Checkbox(done) => rsx! {
            input { r#type: "checkbox", checked: *done, disabled: true }
        },
        Node::Image { href, alt } => match href {
            Some(href) => {
                let label = if alt.is_empty() { href } else { alt };
                rsx! {
                    a { href: "{href}", target: "_blank", rel: "noopener noreferrer", "{label}" }
                }
            }
            None => rsx! { "{alt}" },
        },
        Node::Wrap(kind, children) => match kind {
            Wrap::Paragraph => rsx! { p { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H1) => rsx! { h1 { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H2) => rsx! { h2 { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H3) => rsx! { h3 { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H4) => rsx! { h4 { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H5) => rsx! { h5 { {render_nodes(children)} } },
            Wrap::Heading(HeadingLevel::H6) => rsx! { h6 { {render_nodes(children)} } },
            Wrap::Quote => rsx! { blockquote { {render_nodes(children)} } },
            Wrap::CodeBlock => rsx! { pre { code { {render_nodes(children)} } } },
            Wrap::List(Some(start)) => rsx! { ol { start: "{start}", {render_nodes(children)} } },
            Wrap::List(None) => rsx! { ul { {render_nodes(children)} } },
            Wrap::Item => rsx! { li { {render_nodes(children)} } },
            Wrap::Emphasis => rsx! { em { {render_nodes(children)} } },
            Wrap::Strong => rsx! { strong { {render_nodes(children)} } },
            Wrap::Strikethrough => rsx! { del { {render_nodes(children)} } },
            Wrap::Link(Some(href)) => rsx! {
                a { href: "{href}", target: "_blank", rel: "noopener noreferrer", {render_nodes(children)} }
            },
            Wrap::Table => rsx! { table { {render_nodes(children)} } },
            Wrap::TableHead => rsx! {
                thead {
                    tr {
                        for cell in cells(children) {
                            {render_cell(cell, true)}
                        }
                    }
                }
            },
            Wrap::TableRow => rsx! {
                tr {
                    for cell in cells(children) {
                        {render_cell(cell, false)}
                    }
                }
            },
            _ => render_nodes(children),
        },
    }
}

#[component]
pub fn Markdown(text: String) -> Element {
    let nodes = parse(&text);
    rsx! {
        div { class: "md", {render_nodes(&nodes)} }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn render(text: &str) -> String {
        dioxus_ssr::render_element(rsx! { Markdown { text: text.to_string() } })
    }

    #[test]
    fn shows_each_element() {
        let html = render(
            "# Title\n\nSome **bold** and *italic* and `code`.\n\n- one\n- two\n\n1. first\n\n> a quote\n\n```rust\nlet x = 1;\n```\n\n| head | cell |\n| --- | --- |\n| a | b |\n\n[a link](https://example.com)",
        );
        assert!(html.contains("<h1>Title</h1>"), "{html}");
        assert!(html.contains("<strong>bold</strong>"), "{html}");
        assert!(html.contains("<em>italic</em>"), "{html}");
        assert!(html.contains("<code>code</code>"), "{html}");
        assert!(html.contains("<li>one</li>"), "{html}");
        assert!(html.contains("<ol"), "{html}");
        assert!(
            html.contains("<blockquote><p>a quote</p></blockquote>"),
            "{html}"
        );
        assert!(
            html.contains("<pre><code>let x = 1;\n</code></pre>"),
            "{html}"
        );
        assert!(html.contains("<th>head</th>"), "{html}");
        assert!(html.contains("<td>a</td>"), "{html}");
        assert!(
            html.contains(
                r#"<a href="https://example.com" target="_blank" rel="noopener noreferrer">a link</a>"#
            ),
            "{html}"
        );
    }

    #[test]
    fn blocks_unsafe_html() {
        let html = render(
            "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[click](javascript:alert(1))\n\n![pic](javascript:alert(2))",
        );
        assert!(!html.contains("<script"), "{html}");
        assert!(html.contains("&#60;script&#62;"), "{html}");
        assert!(!html.contains("<img"), "{html}");
        assert!(html.contains("&#60;img"), "{html}");
        assert!(!html.contains("javascript:"), "{html}");
        assert!(html.contains("click"), "{html}");
        assert!(html.contains("pic"), "{html}");
    }

    #[test]
    fn shows_image_as_link() {
        let html = render("![alt text](https://example.com/pic.png)");
        assert!(!html.contains("<img"), "{html}");
        assert!(
            html.contains(
                r#"<a href="https://example.com/pic.png" target="_blank" rel="noopener noreferrer">alt text</a>"#
            ),
            "{html}"
        );
    }
}
