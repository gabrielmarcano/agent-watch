import SwiftUI

struct ReaderDetailView: View {
    var item: HistoryItem
    @Environment(\.presentationMode) var presentationMode
    
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                if let query = item.query, !query.isEmpty {
                    VStack(alignment: .leading) {
                        Text("PROMPT").font(.system(size: 8, weight: .bold)).foregroundColor(.blue)
                        Text(query).font(.system(size: 11))
                    }
                    .padding()
                    .background(Color.white.opacity(0.1))
                    .cornerRadius(12)
                }
                
                Text("AGENT RESPONSE").font(.system(size: 9, weight: .heavy)).foregroundColor(.green)
                
                // TODO: Replace with a SwiftUI Markdown renderer library (e.g. MarkdownUI) 
                // for full markdown support including code blocks and bold/italics.
                Text(MarkdownFormatter.clean(item.response))
                    .font(.system(size: 13))
                    .padding()
                    .background(Color.blue.opacity(0.1))
                    .cornerRadius(12)
                
                Button(action: {
                    // Trigger dictation for follow up
                }) {
                    HStack {
                        Image(systemName: "mic.fill")
                        Text("VOICE DICTATION").bold()
                    }
                }
                .buttonStyle(.borderedProminent)
                .tint(.blue)
                
                Button("DONE READING") {
                    presentationMode.wrappedValue.dismiss()
                }
                .buttonStyle(.bordered)
                .tint(.gray)
            }
            .padding()
        }
    }
}
