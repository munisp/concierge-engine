FROM node:20-alpine AS build
WORKDIR /src
COPY web/console/package.json web/console/package-lock.json ./
RUN npm ci
COPY web/console/ .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
