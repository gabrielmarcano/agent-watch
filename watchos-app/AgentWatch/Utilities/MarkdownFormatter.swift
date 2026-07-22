import Foundation

struct MarkdownFormatter {
    static func clean(_ input: String?) -> String {
        guard let input = input, !input.isEmpty else { return "" }
        var text = input
        
        // Regex patterns to clean markdown
        let patterns = [
            ("```(?:[a-zA-Z]*)\\n?", ""), // code block starts
            ("```", ""),                   // code block ends
            ("\\*\\*([^*]+)\\*\\*", "$1"), // bold
            ("__([^_]+)__", "$1"),         // bold 2
            ("\\*([^*]+)\\*", "$1"),       // italics
            ("_([^_]+)_", "$1"),           // italics 2
            ("`([^`]+)`", "$1"),           // inline code
            ("(?m)^#{1,6}\\s*", ""),       // headers
            ("(?m)^\\s*[-*+]\\s+", "• "),  // lists
            ("\\[([^\\]]+)\\]\\([^\\)]+\\)", "$1"), // links
            ("<[^>]*>", ""),               // html tags
            ("\\n{3,}", "\n\n")            // multiple newlines
        ]
        
        for (pattern, template) in patterns {
            if let regex = try? NSRegularExpression(pattern: pattern, options: []) {
                text = regex.stringByReplacingMatches(
                    in: text,
                    range: NSRange(text.startIndex..., in: text),
                    withTemplate: template
                )
            }
        }
        
        return text.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    
    static func truncate(_ input: String?, maxChars: Int = 140) -> String {
        let cleaned = clean(input)
        if cleaned.count <= maxChars { return cleaned }
        
        let index = cleaned.index(cleaned.startIndex, offsetBy: maxChars)
        let truncated = String(cleaned[..<index])
        
        if let lastSpace = truncated.lastIndex(of: " ") {
            let distance = cleaned.distance(from: cleaned.startIndex, to: lastSpace)
            if distance > maxChars / 2 {
                return String(truncated[..<lastSpace]) + "…"
            }
        }
        return truncated + "…"
    }
}
