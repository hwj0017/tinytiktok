from fastapi import FastAPI, UploadFile, File
from paddleocr import PaddleOCR
import uvicorn
import numpy as np
import cv2

app = FastAPI()

# 初始化 PaddleOCR 引擎
ocr = PaddleOCR(use_angle_cls=True, lang="ch", show_log=False)


@app.post("/ocr")
async def extract_text(image: UploadFile = File(...)):
    try:
        contents = await image.read()
        nparr = np.frombuffer(contents, np.uint8)
        img = cv2.imdecode(nparr, cv2.IMREAD_COLOR)

        result = ocr.ocr(img, cls=True)

        texts = []
        if result and result[0]:
            for line in result[0]:
                texts.append(line[1][0])

        return {"code": 0, "text": " ".join(texts)}
    except Exception as e:
        return {"code": 500, "error": str(e), "text": ""}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000)
