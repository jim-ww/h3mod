// Stable replacement for quickentity-rs' auto_context proc macro.
//
// Upstream rewrites `.ctx` into `.context("<expr> at <file>:<line>")` for
// anyhow, using nightly-only span APIs for the location. This does the same
// rewrite with a fixed message, so behaviour is identical apart from the
// wording of error messages.

use proc_macro::{Delimiter, Group, Ident, Literal, TokenStream, TokenTree};

#[proc_macro_attribute]
pub fn auto_context(_: TokenStream, item: TokenStream) -> TokenStream {
    rewrite(item)
}

fn rewrite(stream: TokenStream) -> TokenStream {
    let mut out = Vec::new();
    let mut after_dot = false;
    for tt in stream {
        let is_dot = matches!(&tt, TokenTree::Punct(p) if p.as_char() == '.');
        match tt {
            TokenTree::Group(g) => {
                let mut ng = Group::new(g.delimiter(), rewrite(g.stream()));
                ng.set_span(g.span());
                out.push(TokenTree::Group(ng));
            }
            TokenTree::Ident(i) if after_dot && i.to_string() == "ctx" => {
                out.push(TokenTree::Ident(Ident::new("context", i.span())));
                let msg = TokenStream::from(TokenTree::Literal(Literal::string("quickentity-rs")));
                out.push(TokenTree::Group(Group::new(Delimiter::Parenthesis, msg)));
            }
            other => out.push(other),
        }
        after_dot = is_dot;
    }
    out.into_iter().collect()
}
