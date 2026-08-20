FROM node:20-alpine
WORKDIR /app
RUN npm install --global npm@10.9.2
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
ENV VITE_API_BASE_INTERNAL=http://backend:8080
EXPOSE 5173
CMD ["npm", "run", "dev"]
