// local-api.js
const express = require('express');
const app = express();
const port = process.env.PORT || 3000;

const largeJson = {
  data: []
};

for (let i = 0; i < 1000; i++) {
  largeJson.data.push({
    id: i,
    name: `Item ${i}`,
    description: 'Lorem ipsum dolor sit amet, consectetur adipiscing elit. '.repeat(5),
    tags: ['test', 'demo', 'json', 'large', 'payload'],
    nested: {
      a: i,
      b: i * 2,
      c: {
        d: i * 3,
        e: `Extra ${i}`
      }
    }
  });
}



app.use(express.json());

// Ruta raíz
app.get('/', (req, res) => {
  res.json(largeJson);
});

// Ruta de eco (query params y body)
app.get('/echo', (req, res) => {
  res.json({ query: req.query, body: req.body });
});

// Ruta POST para recibir datos
app.post('/data', (req, res) => {
  res.json({ recibido: req.body });
});

app.listen(port, () => {
  console.log(`API local escuchando en http://localhost:${port}`);
});

