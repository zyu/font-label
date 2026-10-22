FROM node:22-alpine AS build
ARG NPM_REGISTRY=https://registry.npmjs.org/
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm config set registry "${NPM_REGISTRY}" && npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
