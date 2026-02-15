# Copyright 2025 Changkun Ou. All rights reserved.
# Use of this source code is governed by a MIT
# license that can be found in the LICENSE file.

FROM alpine
RUN apk add --no-cache git openssh
COPY ideas /app/ideas
EXPOSE 80
CMD ["/app/ideas"]
